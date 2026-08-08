// Command openapi writes docs/openapi.json from the same RegisterRoutes the
// server uses. It needs no database and no frontend build, so it can run in a
// CI job that has neither - which is what makes the drift check cheap enough to
// run on every pull request.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/robert-crandall/3d-library/internal/app"
)

// SpecPath is where the generated document is committed, relative to the repo
// root. Run this command from the repo root (make spec does).
const SpecPath = "docs/openapi.json"

func main() {
	// library.NewService write-probes its directory, so spec mode needs a real
	// one. A temp dir keeps the generator from caring about UPLOAD_DIR.
	dir, err := os.MkdirTemp("", "openapi-spec-*")
	if err != nil {
		log.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	out, err := app.SpecJSON(dir)
	if err != nil {
		log.Fatalf("generate: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(SpecPath), 0o755); err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(SpecPath, out, 0o644); err != nil {
		log.Fatalf("write: %v", err)
	}
	log.Printf("wrote %s (%d bytes)", SpecPath, len(out))
}
