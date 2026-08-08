package app_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/robert-crandall/3d-library/internal/app"
)

// specPath is relative because a Go test's working directory is its own package
// directory, not the repo root.
const specPath = "../../docs/openapi.json"

func generate(t *testing.T) []byte {
	t.Helper()
	out, err := app.SpecJSON(t.TempDir())
	if err != nil {
		t.Fatalf("SpecJSON: %v", err)
	}
	return out
}

// The generator runs with nil database pools. If registration ever grows a
// query, this is where it panics - which is the point: the spec job has no
// Postgres, and a panic here is a much better failure than a red CI job with no
// obvious cause.
func TestSpecDescribesTheContract(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(generate(t), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Every operation this template registers, as path + method. Path-only
	// presence isn't enough: /api/files serves both GET and POST, so a
	// foundation bump that dropped only the POST would leave the path behind
	// and a shrunken spec would sail through the drift check, because both
	// sides moved together. Adding an operation never fails this; removing one
	// does, which is the signal worth having on an upgrade.
	//
	// The codes column pins the refusals the login page has to render. If the
	// foundation stops declaring one, the generated TypeScript stops describing
	// it and the UI silently loses a branch.
	for _, tc := range []struct {
		path, method string
		codes        []string
	}{
		// The template's own route. Everything below it is the foundation's.
		{"/api/app", "get", []string{"500"}},
		{"/api/auth/register", "post", []string{"403", "409", "422"}},
		{"/api/auth/login", "post", []string{"401"}},
		{"/api/auth/logout", "post", nil},
		{"/api/auth/me", "get", []string{"401"}},
		{"/api/auth/google/start", "get", nil},
		{"/api/auth/google/callback", "get", []string{"500"}},
		{"/api/files", "get", nil},
		{"/api/files", "post", nil},
		{"/api/files/{id}", "get", nil},
		{"/api/files/{id}", "delete", nil},
		{"/api/files/{id}/thumbnail", "get", nil},
		{"/api/push/subscribe", "post", nil},
		{"/api/push/unsubscribe", "post", nil},
		{"/api/push/test", "post", nil},
		{"/api/push/vapid-public-key", "get", nil},
		{"/api/tokens", "get", nil},
		{"/api/tokens", "post", nil},
		{"/api/tokens/{id}", "delete", nil},
	} {
		op, ok := doc.Paths[tc.path][tc.method]
		if !ok {
			t.Errorf("spec is missing %s %s", tc.method, tc.path)
			continue
		}
		for _, code := range tc.codes {
			if _, ok := op.Responses[code]; !ok {
				t.Errorf("%s %s does not declare a %s response", tc.method, tc.path, code)
			}
		}
	}
}

// A drift check is only a fair test if generation is deterministic. If huma or
// encoding/json ever started emitting keys in map order, this would fail here
// rather than as an unexplainable red `spec` job on someone else's pull request.
func TestSpecIsByteStable(t *testing.T) {
	if first, second := generate(t), generate(t); !bytes.Equal(first, second) {
		t.Errorf("two generations differ: %d bytes vs %d bytes", len(first), len(second))
	}
}

func TestCommittedSpecIsUpToDate(t *testing.T) {
	committed, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v (run `make spec`)", specPath, err)
	}
	if !bytes.Equal(committed, generate(t)) {
		t.Errorf("%s is stale - run `make spec` and commit the result", specPath)
	}
}
