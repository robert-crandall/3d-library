package app_test

import (
	"testing"

	"github.com/robert-crandall/go-home-server/auth"
	"github.com/robert-crandall/go-home-server/server"

	"github.com/robert-crandall/3d-library/internal/app"
)

// Google sign-in is optional: cmd/server leaves Deps.Google nil when no
// GOOGLE_* variable is set, and RegisterRoutes then mounts neither endpoint.
// The library is deliberately NOT optional - it is the app - so this covers the
// one remaining guard.
//
// This is the only check on that guard that runs on every pull request.
//
// Note what is deliberately *not* asserted: that the committed spec loses those
// paths. It doesn't. cmd/openapi always passes a placeholder Google config (see
// SpecModeDeps), so docs/openapi.json describes the whole surface and a
// password-only deployment serves a subset of it.
func TestOptionalRoutesAreSkippedWithoutTheirConfig(t *testing.T) {
	deps, err := app.SpecModeDeps(t.TempDir())
	if err != nil {
		t.Fatalf("SpecModeDeps: %v", err)
	}
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

	// Both optional paths, not one of them: the claim is that these routes are
	// gone, and naming one of two would leave the other unasserted.
	for _, p := range []string{
		"/api/auth/google/start",
		"/api/auth/google/callback",
	} {
		if _, ok := paths[p]; ok {
			t.Errorf("%s is registered without its config", p)
		}
	}

	// Non-vacuity: registration really did run, so the absences above mean
	// something. /api/models is the stronger of the two checks - it is this
	// app's own route and it is not behind any guard.
	for _, p := range []string{"/api/auth/login", "/api/models"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("%s is missing - registration didn't run at all", p)
		}
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
