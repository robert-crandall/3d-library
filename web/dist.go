// Package web embeds the built SvelteKit SPA so the whole app ships as one
// binary.
//
// The `all:` prefix is load-bearing. SvelteKit emits its chunks under `_app/`,
// and a plain `//go:embed build` silently skips directories whose names start
// with an underscore - you would get a binary that serves index.html and 404s
// every script tag.
//
// The consequence is that `go build ./...` fails on a clean checkout until the
// frontend has been built at least once, because the pattern matches nothing.
// That is the build graph, not a bug: run `make setup` (or `make build`, which
// builds the frontend first). Do not commit anything under build/ to make this
// error go away.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:build
var buildFS embed.FS

// Dist is the built SPA rooted at index.html, which is what
// server.Options.SPA expects.
var Dist, _ = fs.Sub(buildFS, "build")
