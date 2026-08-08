// Command server is the app: it loads config, applies the foundation's shared
// migrations and this app's own, wires auth, web push, and the model library
// onto one huma API, and serves that API alongside the embedded SPA on a single
// port.
//
// Routes live in internal/app so cmd/openapi can generate the committed spec
// from the same registration.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/robert-crandall/go-home-server/auth"
	"github.com/robert-crandall/go-home-server/config"
	"github.com/robert-crandall/go-home-server/db"
	sharedmigrations "github.com/robert-crandall/go-home-server/migrations"
	"github.com/robert-crandall/go-home-server/notify"
	"github.com/robert-crandall/go-home-server/server"

	"github.com/robert-crandall/3d-library/internal/app"
	"github.com/robert-crandall/3d-library/internal/library"
	"github.com/robert-crandall/3d-library/migrations"
	"github.com/robert-crandall/3d-library/web"
)

func main() {
	// Subcommands are dispatched before anything else touches config or the
	// database - see healthcheck.go. Anything other than exactly one
	// `healthcheck` argument is an error rather than "ignore it and boot": a
	// typo'd HEALTHCHECK would otherwise start a second full server inside the
	// container, migrations and all, on every probe interval.
	if len(os.Args) > 1 {
		if len(os.Args) != 2 || os.Args[1] != "healthcheck" {
			fmt.Fprintf(os.Stderr, "usage: %s [healthcheck]\n", os.Args[0])
			os.Exit(2)
		}
		os.Exit(runHealthCheck(probeURL(os.Getenv("ADDR"))))
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()

	// Each source tracks its own goose version table, which is why this app's
	// migrations can also start at 00001 without colliding with the shared set.
	if err := db.Migrate(cfg.DatabaseURL,
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
		log.Fatalf("migrate: %v", err)
	}

	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	authSvc := auth.NewService(pool, cfg.IsProduction())
	authSvc.OpenRegistration = cfg.AllowOpenRegistration

	notifySvc, err := notify.NewService(pool, notify.VAPID{
		Public:  cfg.VAPIDPublic,
		Private: cfg.VAPIDPrivate,
		Subject: cfg.VAPIDSubject,
	})
	if err != nil {
		log.Fatalf("notify: %v", err)
	}

	// UPLOAD_DIR is required, unlike in the template it came from: storing and
	// browsing models is the whole app, so a deployment without somewhere to
	// put them has nothing to serve. A missing or unwritable directory is fatal
	// for the same reason it was there - the alternative is writing uploads to
	// a container layer that gets thrown away on the next deploy.
	librarySvc, err := library.NewService(pool, library.Options{Dir: cfg.UploadDir})
	if err != nil {
		// Prefixed with the variable name because library.NewService talks
		// about a "upload dir" it was handed, and the operator reading this log
		// needs to know which knob to turn.
		log.Fatalf("UPLOAD_DIR: %v", err)
	}

	// Sign in with Google, also optional. The gate is "any of the three set"
	// rather than "the client ID is set", so a half-configured app crashes at
	// startup with RegisterGoogle's error instead of quietly booting
	// password-only and leaving someone to wonder where the button went.
	var googleCfg *auth.GoogleConfig
	if cfg.GoogleClientID != "" || cfg.GoogleClientSecret != "" || cfg.GoogleRedirectURL != "" {
		googleCfg = &auth.GoogleConfig{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  cfg.GoogleRedirectURL,
		}
	}

	srv := server.New(server.Options{
		Title:       app.Title,
		Version:     app.Version,
		Addr:        cfg.Addr,
		SPA:         web.Dist,
		Middlewares: []func(http.Handler) http.Handler{authSvc.Middleware},
		HealthCheck: pool.Ping,
		HumaConfig:  authSvc.TokenHumaConfig,
	})

	// Shared with cmd/openapi, which is what keeps the committed spec honest
	// about what RegisterRoutes mounts. Still not a per-deployment manifest:
	// cmd/openapi always passes a Google config, so a password-only deployment
	// serves a subset of the spec it ships.
	if err := app.RegisterRoutes(srv.API, app.Deps{
		Auth:    authSvc,
		Notify:  notifySvc,
		Library: librarySvc,
		Google:  googleCfg,
	}); err != nil {
		log.Fatalf("routes: %v", err)
	}

	log.Printf("listening on %s (env=%s)", cfg.Addr, cfg.Env)
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
}
