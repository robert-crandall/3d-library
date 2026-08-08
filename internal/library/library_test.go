package library

import (
	"os"
	"path/filepath"
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

// A thumbnail that cannot be written must not leave a row claiming it has one,
// and must not fail the upload.
//
// The failure is injected by putting a directory where the sidecar wants to be,
// which os.OpenFile refuses with EISDIR. That is the closest deterministic
// stand-in for the real cases - a full disk, a permission the app does not have
// - and it exercises the same branch. The end-to-end test cannot reach this at
// all: the storage key is random, so no test can know the sidecar's path before
// the upload creates it.
//
// A weaker version would check only that publish returns nil. The load-bearing
// assertion is the second one: insertFile derives has_thumbnail from these
// bytes, so leaving them set is exactly how a row comes to claim a thumbnail
// that is not on disk.
func TestASidecarThatCannotBeWrittenIsForgotten(t *testing.T) {
	dir := t.TempDir()
	s := &Service{dir: dir}

	tmp := filepath.Join(dir, tmpPrefix+"x")
	if err := os.WriteFile(tmp, []byte("solid"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := staged{key: "blob.png", tmpPath: tmp, thumb: []byte("PNG-ish")}
	if err := os.Mkdir(filepath.Join(dir, "blob.png"+thumbSuffix), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := s.publish(&f); err != nil {
		t.Fatalf("publish returned %v; a thumbnail that cannot be written must not fail an upload", err)
	}
	if f.thumb != nil {
		t.Error("thumb is still set after the sidecar failed to write; the row would claim a thumbnail the disk does not have")
	}
	if _, err := os.Stat(filepath.Join(dir, "blob.png")); err != nil {
		t.Errorf("the blob is not published: %v", err)
	}
}

// A refused sidecar write leaves the directory as it found it.
//
// Honest about its reach: this drives the OpenFile branch only. The two later
// branches, where Write or Close fails partway and os.Remove tidies up, need a
// filesystem that fails mid-operation and nothing here can ask for one. They
// stay because a half-written PNG is served to the browser as a broken image
// forever, where an absent one is a placeholder - the cost of being wrong is
// worse than three lines.
func TestWriteSidecarLeavesNothingBehindOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x"+thumbSuffix)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if writeSidecar(path, []byte("PNG-ish")) {
		t.Fatal("writeSidecar reported success writing over a directory")
	}
	// The directory is untouched, and no stray file appeared beside it.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Errorf("directory holds %v, want just the blocking directory", entries)
	}
}
