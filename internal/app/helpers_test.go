package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robert-crandall/go-home-server/auth"
	"github.com/robert-crandall/go-home-server/db"
	sharedmigrations "github.com/robert-crandall/go-home-server/migrations"
	"github.com/robert-crandall/go-home-server/notify"
	"github.com/robert-crandall/go-home-server/server"

	"github.com/robert-crandall/3d-library/internal/app"
	"github.com/robert-crandall/3d-library/internal/library"
	"github.com/robert-crandall/3d-library/migrations"
)

// testPool migrates both migration sources and hands back a clean database.
//
// Both sources, not just the shared one: models references users, so a test
// database that only had the shared set would fail on the first insert with a
// missing-table error that looks nothing like the bug it actually is.
func testPool(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()

	if err := db.Migrate(url,
		db.MigrationSource{
			FS:        sharedmigrations.FS,
			Dir:       sharedmigrations.Dir,
			TableName: sharedmigrations.TableName,
		},
		db.MigrationSource{
			FS:        migrations.FS,
			Dir:       migrations.Dir,
			TableName: migrations.TableName,
		},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := db.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	// Start from nothing, so "the first account" means what it says however
	// this database was left by a previous run. models cascades from users.
	if _, err := pool.Exec(context.Background(), "TRUNCATE users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return pool
}

// newTestServer stands up the real router over a real pool, exactly as
// cmd/server does.
func newTestServer(t *testing.T, pool *pgxpool.Pool, opts library.Options) *httptest.Server {
	return newTestServerWithLibraryPool(t, pool, pool, opts)
}

// newTestServerWithLibraryPool is the same thing with a separate pool behind
// the library, so a test can kill the library's database access without also
// breaking the session lookup that authenticates the request.
func newTestServerWithLibraryPool(t *testing.T, pool, libPool *pgxpool.Pool, opts library.Options) *httptest.Server {
	t.Helper()

	authSvc := auth.NewService(pool, false)
	authSvc.OpenRegistration = true

	notifySvc, err := notify.NewService(pool, notify.VAPID{})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	librarySvc, err := library.NewService(libPool, opts)
	if err != nil {
		t.Fatalf("library: %v", err)
	}

	srv := server.New(server.Options{
		Title:       app.Title,
		Version:     app.Version,
		Middlewares: []func(http.Handler) http.Handler{authSvc.Middleware},
		HumaConfig:  authSvc.TokenHumaConfig,
	})
	if err := app.RegisterRoutes(srv.API, app.Deps{
		Auth: authSvc, Notify: notifySvc, Library: librarySvc,
	}); err != nil {
		t.Fatalf("register routes: %v", err)
	}

	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)
	return ts
}
