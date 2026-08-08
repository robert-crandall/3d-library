// Package migrations holds this app's own goose SQL migrations - the library
// tables. The foundation's shared migrations (users, sessions, tokens) live in
// go-home-server and are applied from their own source with their own version
// table, so both sets can start at 00001 without colliding.
package migrations

import "embed"

// FS is the embedded set of migration files.
//
//go:embed *.sql
var FS embed.FS

// Dir is the directory within FS that contains the migrations. Because the
// files are embedded at the package root, this is ".".
const Dir = "."

// TableName is the goose version table these migrations track themselves in,
// kept separate from the foundation's goose_shared_version.
const TableName = "goose_library_version"
