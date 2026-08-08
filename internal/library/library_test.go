package library

import (
	"strings"
	"testing"
)

// The type mapper is total: every extension lands on one of the seven types.
// That totality is why the database carries no CHECK constraint, so it is the
// property worth pinning. A test that only checked ".stl" would pass against a
// mapper that panicked on everything else.
func TestFileType(t *testing.T) {
	for _, tc := range []struct{ filename, want string }{
		{"benchy.stl", "stl"},
		{"BENCHY.STL", "stl"},
		{"plate.3mf", "3mf"},
		{"print.gcode", "gcode"},
		{"print.gco", "gcode"},
		{"print.bgcode", "gcode"},
		{"part.step", "step"},
		{"part.stp", "step"},
		{"mesh.obj", "obj"},
		{"photo.jpg", "image"},
		{"photo.JPEG", "image"},
		{"icon.svg", "image"},
		{"notes.txt", "document"},
		{"readme.md", "document"},
		{"license", "document"},
		{"archive.tar.gz", "document"},
		{"", "document"},
		{".stl", "stl"},
	} {
		if got := fileType(tc.filename); got != tc.want {
			t.Errorf("fileType(%q) = %q, want %q", tc.filename, got, tc.want)
		}
	}
}

// A storage key is what a client filename can never influence, so what matters
// is that no input produces a path segment. Testing only the extension would
// miss the traversal cases entirely.
func TestSanitizeExt(t *testing.T) {
	for _, tc := range []struct{ filename, want string }{
		{"benchy.stl", ".stl"},
		{"BENCHY.STL", ".stl"},
		{"noext", ""},
		{"trailing.", ""},
		{"weird.st l", ""},
		{"traversal../..", ""},
		{"long.abcdefghij", ""}, // over 8 characters
		{"unicode.stl\u00e9", ""},
		{"digits.7z", ".7z"},
	} {
		if got := sanitizeExt(tc.filename); got != tc.want {
			t.Errorf("sanitizeExt(%q) = %q, want %q", tc.filename, got, tc.want)
		}
	}
}

// displayName has to survive a hand-rolled client, not just a browser. The
// backslash case is the one a Linux-only test would miss: filepath.Base is a
// no-op on backslashes there, which is why path.Base is used instead.
func TestDisplayName(t *testing.T) {
	for _, tc := range []struct{ filename, want string }{
		{"benchy.stl", "benchy.stl"},
		{"/etc/passwd", "passwd"},
		{"../../escape.stl", "escape.stl"},
		{`..\..\escape.stl`, "escape.stl"},
		{`C:\Users\me\benchy.stl`, "benchy.stl"},
		{"", "upload"},
		{"/", "upload"},
		{".", "upload"},
		{"..", "upload"},
		{"\xff\xfe", "upload"}, // invalid UTF-8 all the way through
	} {
		if got := displayName(tc.filename); got != tc.want {
			t.Errorf("displayName(%q) = %q, want %q", tc.filename, got, tc.want)
		}
	}

	// Long names are cut on a rune boundary, because a split rune would be
	// invalid UTF-8 again and Postgres rejects that in a text column.
	long := strings.Repeat("\u00e9", 400) + ".stl"
	got := displayName(long)
	if len(got) > maxFilenameBytes {
		t.Errorf("displayName kept %d bytes, want <= %d", len(got), maxFilenameBytes)
	}
	if !strings.ContainsRune(got, '\u00e9') || strings.ContainsRune(got, '\ufffd') {
		t.Errorf("displayName(%d-byte name) = %q, want a clean prefix", len(long), got)
	}
}

// Two uploads of the same filename must not collide, because the key is what
// keeps them apart on disk.
func TestStorageKeyIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		key, err := storageKey("benchy.stl")
		if err != nil {
			t.Fatalf("storageKey: %v", err)
		}
		if !strings.HasSuffix(key, ".stl") {
			t.Errorf("key %q lost its extension", key)
		}
		if seen[key] {
			t.Fatalf("duplicate storage key %q", key)
		}
		seen[key] = true
	}
}

// The request cap has to exceed what a legal upload can be, or the guard meant
// to stop an endless preamble would start rejecting valid uploads instead. One
// request carries one file, so the bar is one file plus its framing.
func TestMaxBodyBytesExceedsALegalUpload(t *testing.T) {
	if got := maxBodyBytes(MaxFileBytes); got <= MaxFileBytes {
		t.Errorf("maxBodyBytes = %d, want more than %d", got, MaxFileBytes)
	}
}
