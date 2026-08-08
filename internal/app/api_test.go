package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/robert-crandall/go-home-server/auth"
	"github.com/robert-crandall/go-home-server/db"
	"github.com/robert-crandall/go-home-server/files"
	"github.com/robert-crandall/go-home-server/migrations"
	"github.com/robert-crandall/go-home-server/notify"
	"github.com/robert-crandall/go-home-server/server"

	"github.com/robert-crandall/3d-library/internal/app"
)

// The exact strings the login page renders. The SPA shows the server's `detail`
// verbatim rather than keeping its own copy, so if the foundation reworded one
// of these the UI would quietly start saying something new. This is where that
// gets noticed.
//
// The 409 is here rather than in a browser test because it is unreachable
// from a browser: with the default first-user-only gate, registerUser checks
// the gate before it checks for a duplicate, so a second registration is always
// 403. Reaching 409 needs open registration, and standing up a second server
// and database just for it is more machinery than one assertion is worth.
func TestAuthRefusalStrings(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	if err := db.Migrate(url, db.MigrationSource{
		FS:        migrations.FS,
		Dir:       migrations.Dir,
		TableName: migrations.TableName,
	}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := db.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	// Start from nothing, so "the first account" means what it says however
	// this database was left by a previous run.
	if _, err := pool.Exec(ctx, "TRUNCATE users CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	newServer := func(openRegistration bool) *httptest.Server {
		t.Helper()
		authSvc := auth.NewService(pool, false)
		authSvc.OpenRegistration = openRegistration

		notifySvc, err := notify.NewService(pool, notify.VAPID{})
		if err != nil {
			t.Fatalf("notify: %v", err)
		}
		filesSvc, err := files.NewService(pool, files.Options{Dir: t.TempDir()})
		if err != nil {
			t.Fatalf("files: %v", err)
		}

		srv := server.New(server.Options{
			Title:       app.Title,
			Version:     app.Version,
			Middlewares: []func(http.Handler) http.Handler{authSvc.Middleware},
			HumaConfig:  authSvc.TokenHumaConfig,
		})
		if err := app.RegisterRoutes(srv.API, app.Deps{Auth: authSvc, Notify: notifySvc, Files: filesSvc}); err != nil {
			t.Fatalf("register routes: %v", err)
		}

		ts := httptest.NewServer(srv.Router)
		t.Cleanup(ts.Close)
		return ts
	}

	post := func(ts *httptest.Server, path, email, password string) (int, string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"email": email, "password": password})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		resp, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("post %s: %v", path, err)
		}
		defer resp.Body.Close()

		var problem struct {
			Detail string `json:"detail"`
		}
		// A 200 has no detail field; that is fine, the callers that care about
		// the string only look at failures.
		_ = json.NewDecoder(resp.Body).Decode(&problem)
		return resp.StatusCode, problem.Detail
	}

	const password = "correct-horse-battery"

	// Registration open, so the duplicate check is the thing that fires.
	open := newServer(true)
	if code, _ := post(open, "/api/auth/register", "first@example.com", password); code != http.StatusOK {
		t.Fatalf("registering the first user: got %d, want 200", code)
	}

	for _, tc := range []struct {
		name       string
		ts         *httptest.Server
		path       string
		email      string
		password   string
		wantStatus int
		wantDetail string
	}{
		{
			name: "duplicate email", ts: open, path: "/api/auth/register",
			email: "first@example.com", password: password,
			wantStatus: http.StatusConflict, wantDetail: "email already registered",
		},
		{
			name: "wrong password", ts: open, path: "/api/auth/login",
			email: "first@example.com", password: "wrong-but-long-enough",
			wantStatus: http.StatusUnauthorized, wantDetail: "invalid email or password",
		},
		{
			name: "unknown email", ts: open, path: "/api/auth/login",
			email: "nobody@example.com", password: password,
			wantStatus: http.StatusUnauthorized, wantDetail: "invalid email or password",
		},
		{
			// The default gate, and the one the integration test exercises. Kept
			// here too so all three strings are pinned in one place.
			name: "registration closed", ts: newServer(false), path: "/api/auth/register",
			email: "second@example.com", password: password,
			wantStatus: http.StatusForbidden, wantDetail: "registration is closed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, detail := post(tc.ts, tc.path, tc.email, tc.password)
			if code != tc.wantStatus {
				t.Errorf("status: got %d, want %d", code, tc.wantStatus)
			}
			if detail != tc.wantDetail {
				t.Errorf("detail: got %q, want %q", detail, tc.wantDetail)
			}
		})
	}
}
