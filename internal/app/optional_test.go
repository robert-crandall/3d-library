package app_test

import (
	"testing"

	"github.com/robert-crandall/go-home-server/auth"
	"github.com/robert-crandall/go-home-server/server"

	"github.com/robert-crandall/3d-library/internal/app"
)

// Uploads and Google sign-in are optional: cmd/server leaves Deps.Files nil
// when UPLOAD_DIR is unset and Deps.Google nil when no GOOGLE_* variable is,
// and RegisterRoutes then mounts neither set of endpoints.
//
// This is the only check on those guards that runs on every pull request.
// scripts/docker-smoke.sh proves the same thing about files against a real
// container, but it isn't in CI - so if a guard is deleted, this is what
// catches it.
//
// Note what is deliberately *not* asserted: that the committed spec loses those
// paths. It doesn't. cmd/openapi always passes a real files service and a
// placeholder Google config (see SpecModeDeps), so docs/openapi.json describes
// the template's whole surface and a deployment with either off serves a subset
// of it.
func TestOptionalRoutesAreSkippedWithoutTheirConfig(t *testing.T) {
	deps, err := app.SpecModeDeps(t.TempDir())
	if err != nil {
		t.Fatalf("SpecModeDeps: %v", err)
	}
	deps.Files = nil
	deps.Google = nil

	// The same wiring SpecJSON uses. HumaConfig is not optional here:
	// RegisterTokens re-checks the finished config and panics without it, which
	// would fail this test for a reason that has nothing to do with files.
	srv := server.New(server.Options{
		Title:      app.Title,
		Version:    app.Version,
		HumaConfig: deps.Auth.TokenHumaConfig,
	})
	if err := app.RegisterRoutes(srv.API, deps); err != nil {
		t.Fatalf("register routes: %v", err)
	}

	paths := srv.API.OpenAPI().Paths

	// Every optional path, not a representative sample: the claim is that these
	// routes are gone, and naming three of four would leave one unasserted.
	for _, p := range []string{
		"/api/files",
		"/api/files/{id}",
		"/api/files/{id}/thumbnail",
		"/api/auth/google/start",
		"/api/auth/google/callback",
	} {
		if _, ok := paths[p]; ok {
			t.Errorf("%s is registered without its config", p)
		}
	}

	// Non-vacuity: registration really did run, so the absences above mean
	// something.
	if _, ok := paths["/api/auth/login"]; !ok {
		t.Error("/api/auth/login is missing - registration didn't run at all")
	}
}

// The other half of the Google gate. cmd/server sets Deps.Google when *any* of
// the three variables is present rather than when the client ID is, which is
// only the right call if the incomplete config then stops the process - so this
// pins the error actually coming back rather than RegisterRoutes swallowing it.
//
// The check itself lives upstream in newGoogleAuth; what's asserted here is
// this template's plumbing, which is the part a refactor could quietly drop.
func TestRegisterRoutesRejectsAHalfConfiguredGoogle(t *testing.T) {
	deps, err := app.SpecModeDeps(t.TempDir())
	if err != nil {
		t.Fatalf("SpecModeDeps: %v", err)
	}
	deps.Google = &auth.GoogleConfig{ClientID: "only-the-client-id"}

	srv := server.New(server.Options{
		Title:      app.Title,
		Version:    app.Version,
		HumaConfig: deps.Auth.TokenHumaConfig,
	})
	if err := app.RegisterRoutes(srv.API, deps); err == nil {
		t.Fatal("registered a Google config with no secret and no redirect URL")
	}
}
