package app_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"slices"
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

	// Every operation this app registers, as path + method. Path-only presence
	// isn't enough: /api/models serves both GET and POST, so a change that
	// dropped only the POST would leave the path behind and a shrunken spec
	// would sail through the drift check, because both sides moved together.
	// Adding an operation never fails this; removing one does, which is the
	// signal worth having on a foundation upgrade.
	//
	// The codes column pins the refusals the login page has to render. If the
	// foundation stops declaring one, the generated TypeScript stops describing
	// it and the UI silently loses a branch.
	//
	// The success column exists because the codes column above only asserts
	// that a response is *present*. An upload that answers 201 while the spec
	// promises 200 passes every check here without it, and a generated client
	// believes the spec.
	for _, tc := range []struct {
		path, method string
		success      string
		codes        []string
	}{
		// This app's own routes. Everything below them is the foundation's.
		{"/api/app", "get", "200", []string{"500"}},
		{"/api/models", "get", "200", []string{"401"}},
		{"/api/models", "post", "201", []string{"401", "413", "422"}},
		{"/api/models/{id}", "get", "200", []string{"401", "404"}},
		{"/api/models/{id}", "put", "200", []string{"401", "404", "422"}},
		{"/api/models/{id}", "delete", "204", []string{"401", "404"}},
		{"/api/models/{id}/files", "post", "201", []string{"401", "404", "413", "422"}},
		{"/api/models/{id}/files/{fileId}", "get", "200", []string{"401", "404"}},
		{"/api/models/{id}/files/{fileId}", "delete", "204", []string{"401", "404"}},
		{"/api/models/{id}/thumbnail", "put", "200", []string{"401", "404", "422"}},
		{"/api/models/{id}/parent", "put", "204", []string{"401", "404", "422"}},
		{"/api/library/counts", "get", "200", []string{"401"}},
		{"/api/categories", "get", "200", []string{"401"}},
		{"/api/categories", "post", "201", []string{"401", "422"}},
		{"/api/categories/{id}", "put", "200", []string{"401", "404", "422"}},
		{"/api/categories/{id}", "delete", "204", []string{"401", "404"}},
		{"/api/tags", "get", "200", []string{"401"}},
		{"/api/tags", "post", "201", []string{"401", "422"}},
		{"/api/tags/{id}", "put", "200", []string{"401", "404", "422"}},
		{"/api/tags/{id}", "delete", "204", []string{"401", "404"}},
		{"/api/materials", "get", "200", []string{"401"}},
		{"/api/materials", "post", "201", []string{"401", "422"}},
		{"/api/materials/{id}", "put", "200", []string{"401", "404", "422"}},
		{"/api/materials/{id}", "delete", "204", []string{"401", "404"}},
		{"/api/auth/register", "post", "", []string{"403", "409", "422"}},
		{"/api/auth/login", "post", "", []string{"401"}},
		{"/api/auth/logout", "post", "", nil},
		{"/api/auth/me", "get", "", []string{"401"}},
		{"/api/auth/google/start", "get", "", nil},
		{"/api/auth/google/callback", "get", "", []string{"500"}},
		{"/api/push/subscribe", "post", "", nil},
		{"/api/push/unsubscribe", "post", "", nil},
		{"/api/push/test", "post", "", nil},
		{"/api/push/vapid-public-key", "get", "", nil},
		{"/api/tokens", "get", "", nil},
		{"/api/tokens", "post", "", nil},
		{"/api/tokens/{id}", "delete", "", nil},
	} {
		op, ok := doc.Paths[tc.path][tc.method]
		if !ok {
			t.Errorf("spec is missing %s %s", tc.method, tc.path)
			continue
		}
		if tc.success != "" {
			if _, ok := op.Responses[tc.success]; !ok {
				t.Errorf("%s %s does not declare its %s success response; it declares %v",
					tc.method, tc.path, tc.success, slices.Sorted(maps.Keys(op.Responses)))
			}
		}
		for _, code := range tc.codes {
			if _, ok := op.Responses[code]; !ok {
				t.Errorf("%s %s does not declare a %s response", tc.method, tc.path, code)
			}
		}
	}
}

// The download route is the only operation whose body huma cannot infer:
// huma.StreamResponse carries no type, so without an explicit Responses map the
// spec declares a 200 with no content at all and a generated client has nothing
// to call. The table above would not notice - it only asks whether a 200 is
// *present*, which it is either way. This asks what is in it.
func TestSpecDeclaresTheDownloadBody(t *testing.T) {
	// Decoded one path at a time rather than all of them into one shape: other
	// operations declare a nullable array body, whose "type" is a list rather
	// than a string, and a struct covering every path would fail to unmarshal
	// on those instead of measuring this one.
	var doc struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(generate(t), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	const path = "/api/models/{id}/files/{fileId}"
	var item struct {
		Get struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema struct {
						Type   string `json:"type"`
						Format string `json:"format"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"get"`
	}
	if err := json.Unmarshal(doc.Paths[path], &item); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}

	body, ok := item.Get.Responses["200"].Content["application/octet-stream"]
	if !ok {
		t.Fatalf("GET %s declares no application/octet-stream body; it declares %v",
			path, slices.Sorted(maps.Keys(item.Get.Responses["200"].Content)))
	}
	if body.Schema.Type != "string" || body.Schema.Format != "binary" {
		t.Errorf("download body schema is %s/%s, want string/binary",
			body.Schema.Type, body.Schema.Format)
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
