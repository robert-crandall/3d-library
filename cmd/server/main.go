// Command server is the app: it loads config, applies the foundation's shared
// migrations, wires auth, web push, and file uploads onto one huma API, and
// serves that API alongside the embedded SPA on a single port.
//
// It starts life as a copy of go-home-server's examples/minimal/main.go, with
// the SPA wired in. Routes live in internal/app so cmd/openapi can generate the
// committed spec from the same registration - add your own there.
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
	"github.com/robert-crandall/go-home-server/files"
	"github.com/robert-crandall/go-home-server/migrations"
	"github.com/robert-crandall/go-home-server/notify"
	"github.com/robert-crandall/go-home-server/server"

	"github.com/robert-crandall/3d-library/internal/app"
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

	// Each migration source tracks its own goose version table, so an app's own
	// migrations can also start at 00001. A real app adds a second source:
	//
	//	db.MigrationSource{FS: myapp.MigrationsFS, Dir: "migrations"}
	if err := db.Migrate(cfg.DatabaseURL, db.MigrationSource{
		FS:        migrations.FS,
		Dir:       migrations.Dir,
		TableName: migrations.TableName,
	}); err != nil {
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

	// Uploads are optional. An app that never stores files leaves UPLOAD_DIR
	// unset and simply doesn't serve the file routes - see RegisterRoutes.
	//
	// When it IS set, a missing or unwritable directory is fatal on purpose:
	// the alternative is writing uploads to a container layer that gets thrown
	// away on the next deploy.
	var filesSvc *files.Service
	if cfg.UploadDir != "" {
		filesSvc, err = files.NewService(pool, files.Options{
			Dir:      cfg.UploadDir,
			MaxBytes: cfg.UploadMaxBytes,
		})
		if err != nil {
			log.Fatal(err)
		}
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
	// about what RegisterRoutes mounts. Not a per-deployment manifest, though:
	// cmd/openapi always passes a real files service, so with UPLOAD_DIR unset
	// this binary serves a subset of the spec it ships.
	if err := app.RegisterRoutes(srv.API, app.Deps{
		Auth:   authSvc,
		Notify: notifySvc,
		Files:  filesSvc,
		Google: googleCfg,
	}); err != nil {
		log.Fatalf("routes: %v", err)
	}

	log.Printf("listening on %s (env=%s)", cfg.Addr, cfg.Env)
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
}
