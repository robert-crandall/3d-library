package app

import (
	"encoding/json"
	"fmt"

	"github.com/robert-crandall/go-home-server/auth"
	"github.com/robert-crandall/go-home-server/files"
	"github.com/robert-crandall/go-home-server/notify"
	"github.com/robert-crandall/go-home-server/server"
)

// SpecModeDeps builds the services with no database behind them, for generating
// the OpenAPI document. Registration describes routes and captures its
// dependencies but never calls them, so a nil pool is never dereferenced.
//
// The services still have to be constructed rather than left nil: huma reads
// method values off them at registration time. dir must be a writable directory
// - files.NewService stats it and write-probes it - so callers pass a temp dir.
func SpecModeDeps(dir string) (Deps, error) {
	filesSvc, err := files.NewService(nil, files.Options{Dir: dir})
	if err != nil {
		return Deps{}, fmt.Errorf("files: %w", err)
	}

	// An empty VAPID config skips key validation, which is what we want: the
	// push routes are in the contract either way.
	notifySvc, err := notify.NewService(nil, notify.VAPID{})
	if err != nil {
		return Deps{}, fmt.Errorf("notify: %w", err)
	}

	return Deps{
		Auth:   auth.NewService(nil, true),
		Notify: notifySvc,
		Files:  filesSvc,
		// Placeholder credentials, never dialled: spec mode registers routes
		// and stops. newGoogleAuth only insists that all three are non-empty
		// (the success/failure paths default to "/" and "/login"), so this is
		// enough to keep the Google operations in the committed spec.
		Google: &auth.GoogleConfig{
			ClientID:     "spec-mode-client-id",
			ClientSecret: "spec-mode-client-secret",
			RedirectURL:  "https://spec.invalid/api/auth/google/callback",
		},
	}, nil
}

// SpecJSON renders the OpenAPI document exactly as it is committed to
// docs/openapi.json, trailing newline included.
//
// cmd/openapi and the drift test both go through here, so they cannot disagree
// about formatting - which is the only reason a byte-for-byte drift check is a
// fair test rather than a formatting trap.
func SpecJSON(dir string) ([]byte, error) {
	deps, err := SpecModeDeps(dir)
	if err != nil {
		return nil, err
	}

	// No SPA (server.New panics on one without an index.html, and there is no
	// frontend build to point at here) and no Addr, because nothing listens.
	srv := server.New(server.Options{
		Title:      Title,
		Version:    Version,
		HumaConfig: deps.Auth.TokenHumaConfig,
	})
	if err := RegisterRoutes(srv.API, deps); err != nil {
		return nil, fmt.Errorf("register routes: %w", err)
	}

	out, err := json.MarshalIndent(srv.API.OpenAPI(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return append(out, '\n'), nil
}
