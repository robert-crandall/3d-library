package app_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robert-crandall/3d-library/internal/library"
)

// thumbFixture reads one of the extraction package's fixtures. Sharing them
// rather than making new ones is deliberate: those are real slicer output, and
// an integration test that uploaded invented bytes would prove the HTTP wiring
// against a file no printer ever wrote.
func thumbFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "thumb", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// fetchPNG gets a URL and insists the body really is a PNG. Asserting the
// status alone would pass on an HTML error page served with the wrong type.
func fetchPNG(t *testing.T, c *client, path string) (int, int) {
	t.Helper()
	resp, body := c.get(path)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get %s: got %d: %s", path, resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type %q, want image/png", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, no-cache" {
		t.Errorf("Cache-Control %q, want private, no-cache: a browser cache keys on URL, not session", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options %q, want nosniff", got)
	}
	cfg, format, err := image.DecodeConfig(strings.NewReader(body))
	if err != nil || format != "png" {
		t.Fatalf("body is not a PNG (%s): %v", format, err)
	}
	return cfg.Width, cfg.Height
}

// setThumbnail pins a file, or clears the pin when fileID is nil.
func setThumbnail(t *testing.T, c *client, modelID int64, fileID *int64) (*http.Response, string) {
	t.Helper()
	body := `{"fileId":null}`
	if fileID != nil {
		body = fmt.Sprintf(`{"fileId":%d}`, *fileID)
	}
	return c.send(http.MethodPut, fmt.Sprintf("/api/models/%d/thumbnail", modelID), body)
}

func fileNamed(t *testing.T, m library.ModelDetail, name string) library.File {
	t.Helper()
	for _, f := range m.Files {
		if f.Filename == name {
			return f
		}
	}
	t.Fatalf("model has no file named %q", name)
	return library.File{}
}

// The whole feature end to end: upload files a real slicer wrote, and the
// server extracts, stores, resolves and serves a thumbnail without being asked.
// Checking only that hasThumbnail is true would pass even if the PNG on disk
// were empty, so the bytes are fetched and decoded too.
func TestThumbnailsAreExtractedOnUpload(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "thumbs@example.com")

	resp, body := c.upload("Benchy", map[string]string{
		"a-model.3mf": thumbFixture(t, "prusaslicer.3mf"),
		"b-notes.txt": "print at 0.2mm",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d, want 201: %s", resp.StatusCode, body)
	}
	m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)))

	archive := fileNamed(t, m, "a-model.3mf")
	notes := fileNamed(t, m, "b-notes.txt")
	if !archive.HasThumbnail {
		t.Error("the 3MF has no thumbnail; its Metadata/thumbnail.png should have been extracted")
	}
	if notes.HasThumbnail {
		t.Error("a text file was given a thumbnail")
	}

	if m.ThumbnailFileID == nil || *m.ThumbnailFileID != archive.ID {
		t.Fatalf("model thumbnail = %v, want the 3MF (%d)", m.ThumbnailFileID, archive.ID)
	}
	if !m.ThumbnailAutomatic {
		t.Error("thumbnailAutomatic is false, but nothing was pinned")
	}

	w, h := fetchPNG(t, c, fmt.Sprintf("/api/models/%d/files/%d/thumbnail", m.ID, archive.ID))
	if w != 256 || h != 256 {
		t.Errorf("served %dx%d, want 256x256", w, h)
	}

	// A file with no thumbnail is a 404, not an empty 200: the client uses the
	// status to decide whether to show a placeholder.
	if resp, _ := c.get(fmt.Sprintf("/api/models/%d/files/%d/thumbnail", m.ID, notes.ID)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("thumbnail of a text file: got %d, want 404", resp.StatusCode)
	}

	// Two blobs, one sidecar. A test that only counted blobs would not notice a
	// sidecar written for the text file, or one written twice.
	final, sidecars, temp := blobs(t, dir)
	if len(final) != 2 || len(sidecars) != 1 || len(temp) != 0 {
		t.Errorf("dir has %d blobs, %d sidecars, %d temps; want 2, 1, 0", len(final), len(sidecars), len(temp))
	}
}

// The automatic rule, over one model holding one of each. The order is the
// epic's - an uploaded image beats a slicer's embedded render - and the
// assertion names the file rather than the index so a reordering of the table
// cannot make it pass by accident.
func TestThumbnailPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
		why   string
	}{
		{
			name: "image beats everything",
			files: map[string]string{
				"a.gcode": thumbFixture(t, "orcaslicer_1.5.gcode"),
				"b.3mf":   thumbFixture(t, "prusaslicer.3mf"),
				"c.png":   thumbFixture(t, "render.png"),
			},
			want: "c.png",
			why:  "a photo the user added beats a render the slicer made, even uploaded last",
		},
		{
			name: "3MF beats G-code",
			files: map[string]string{
				"a.gcode": thumbFixture(t, "orcaslicer_1.5.gcode"),
				"b.3mf":   thumbFixture(t, "prusaslicer.3mf"),
			},
			want: "b.3mf",
			why:  "the project file is closer to the model than one sliced instance of it",
		},
		{
			name: "G-code when it is all there is",
			files: map[string]string{
				"a.gcode": thumbFixture(t, "orcaslicer_1.5.gcode"),
				"b.stl":   "solid benchy",
			},
			want: "a.gcode",
			why:  "an STL has no thumbnail, so the G-code's embedded render is the only candidate",
		},
		{
			name: "oldest of a type wins",
			files: map[string]string{
				"a.png": thumbFixture(t, "render.png"),
				"b.jpg": thumbFixture(t, "photo.jpg"),
			},
			want: "a.png",
			why:  "a later upload must not silently move the tile",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbURL := testDatabase(t)
			pool := testPool(t, dbURL)
			ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
			c := signIn(t, ts, "precedence@example.com")

			resp, body := c.upload("Mixed", tc.files)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
			}
			m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)))

			want := fileNamed(t, m, tc.want)
			if m.ThumbnailFileID == nil {
				t.Fatalf("no thumbnail chosen, want %s (%s)", tc.want, tc.why)
			}
			if *m.ThumbnailFileID != want.ID {
				var got string
				for _, f := range m.Files {
					if f.ID == *m.ThumbnailFileID {
						got = f.Filename
					}
				}
				t.Errorf("chose %s, want %s (%s)", got, tc.want, tc.why)
			}
		})
	}
}

// Pinning overrides the rule, and clearing the pin gives it back. The pin has
// to survive a later upload that would have won the automatic order, which is
// the whole reason an override exists.
func TestPinThumbnailOverridesAndClears(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "pin@example.com")

	resp, body := c.upload("Pinned", map[string]string{
		"a.gcode": thumbFixture(t, "orcaslicer_1.5.gcode"),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	m := decodeModel(t, body)
	gcodeFile := fileNamed(t, m, "a.gcode")

	resp, body = setThumbnail(t, c, m.ID, &gcodeFile.ID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pin: got %d: %s", resp.StatusCode, body)
	}
	pinned := decodeModel(t, body)
	if pinned.ThumbnailFileID == nil || *pinned.ThumbnailFileID != gcodeFile.ID {
		t.Fatalf("after pinning, thumbnail = %v, want %d", pinned.ThumbnailFileID, gcodeFile.ID)
	}
	if pinned.ThumbnailAutomatic {
		t.Error("thumbnailAutomatic is true right after an explicit pin")
	}

	// The image would win the automatic order. The pin must beat it.
	ct, part := filePart(t, "b.png", thumbFixture(t, "render.png"))
	if resp, body := c.addFile(m.ID, ct, part); resp.StatusCode != http.StatusCreated {
		t.Fatalf("add image: got %d: %s", resp.StatusCode, body)
	}
	after := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", m.ID)))
	if after.ThumbnailFileID == nil || *after.ThumbnailFileID != gcodeFile.ID {
		t.Errorf("after uploading an image, thumbnail = %v, want the pinned G-code (%d)", after.ThumbnailFileID, gcodeFile.ID)
	}

	// The grid too, and this is the assertion that costs something: the list
	// resolves the pin down a different code path from the detail screen, and
	// it is the one where a resolver that only ever runs the automatic rule
	// still looks right in every other test here. The pinned G-code and the
	// automatic pick are deliberately different files at this point.
	grid := decodeList(t, mustGet(t, c, "/api/models")).Items
	if len(grid) != 1 || grid[0].ThumbnailFileID == nil || *grid[0].ThumbnailFileID != gcodeFile.ID {
		t.Errorf("grid shows %v, want the pinned G-code (%d)", grid, gcodeFile.ID)
	}

	// And clearing hands it back to the rule, which now prefers the image.
	resp, body = setThumbnail(t, c, m.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear: got %d: %s", resp.StatusCode, body)
	}
	cleared := decodeModel(t, body)
	image := fileNamed(t, cleared, "b.png")
	if cleared.ThumbnailFileID == nil || *cleared.ThumbnailFileID != image.ID {
		t.Errorf("after clearing, thumbnail = %v, want the image (%d)", cleared.ThumbnailFileID, image.ID)
	}
	if !cleared.ThumbnailAutomatic {
		t.Error("thumbnailAutomatic is false after clearing the pin")
	}
}

// The pin is a write, so it gets the same refusals every other write gets. The
// two 422s are the interesting ones: they are the cases a foreign key alone
// would report as a 500.
func TestPinThumbnailRefusals(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "refuse@example.com")

	_, body := c.upload("Mine", map[string]string{
		"a.png": thumbFixture(t, "render.png"),
		"b.stl": "solid benchy",
	})
	mine := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)))
	stl := fileNamed(t, mine, "b.stl")

	_, body = c.upload("Other model", map[string]string{"c.png": thumbFixture(t, "render.png")})
	other := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)))
	otherFile := fileNamed(t, other, "c.png")

	stranger := signIn(t, ts, "stranger@example.com")

	for _, tc := range []struct {
		name    string
		client  *client
		modelID int64
		fileID  int64
		want    int
		why     string
	}{
		{"a file with no thumbnail", c, mine.ID, stl.ID, http.StatusUnprocessableEntity,
			"an STL has nothing to show, so pinning it would blank the tile"},
		{"a file of another model", c, mine.ID, otherFile.ID, http.StatusUnprocessableEntity,
			"the file exists and has a thumbnail, so only the model check catches it"},
		{"a file that does not exist", c, mine.ID, 999999, http.StatusUnprocessableEntity,
			"a dangling id is the caller's mistake, not a server fault"},
		{"a model that does not exist", c, 999999, stl.ID, http.StatusNotFound,
			"the model is the resource in the path, so it is a 404"},
		{"somebody else's model", stranger, mine.ID, stl.ID, http.StatusNotFound,
			"a 403 would confirm the model exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := tc.fileID
			resp, body := setThumbnail(t, tc.client, tc.modelID, &id)
			if resp.StatusCode != tc.want {
				t.Fatalf("got %d, want %d (%s): %s", resp.StatusCode, tc.want, tc.why, body)
			}
			// The refusal must not leak a storage key or an upload path, the
			// same rule every other handler in this package follows.
			if strings.Contains(body, os.TempDir()) || strings.Contains(body, ".thumb") {
				t.Errorf("error body leaks a path: %s", body)
			}
		})
	}

	// And none of that changed anything.
	after := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", mine.ID)))
	png := fileNamed(t, after, "a.png")
	if after.ThumbnailFileID == nil || *after.ThumbnailFileID != png.ID || !after.ThumbnailAutomatic {
		t.Errorf("a rejected pin changed the model: thumbnail = %v, automatic = %v", after.ThumbnailFileID, after.ThumbnailAutomatic)
	}
}

// Deleting the pinned file must leave the model showing something. This is the
// case the ON DELETE SET NULL exists for: the row that pointed at the deleted
// file has to lose the pin, not keep a dangling id, and the rule then picks the
// next candidate.
func TestDeletingThePinnedFileFallsBackToAutomatic(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "unpin@example.com")

	_, body := c.upload("Two pictures", map[string]string{
		"a.png":   thumbFixture(t, "render.png"),
		"b.gcode": thumbFixture(t, "orcaslicer_1.5.gcode"),
	})
	m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)))
	png := fileNamed(t, m, "a.png")
	gcodeFile := fileNamed(t, m, "b.gcode")

	if _, body := setThumbnail(t, c, m.ID, &gcodeFile.ID); body == "" {
		t.Fatal("pin returned an empty body")
	}

	if resp, body := c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d/files/%d", m.ID, gcodeFile.ID), ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete pinned file: got %d: %s", resp.StatusCode, body)
	}

	after := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", m.ID)))
	if after.ThumbnailFileID == nil {
		t.Fatal("model lost its thumbnail entirely; the PNG should have taken over")
	}
	if *after.ThumbnailFileID != png.ID {
		t.Errorf("thumbnail = %d, want the remaining PNG (%d)", *after.ThumbnailFileID, png.ID)
	}
	if !after.ThumbnailAutomatic {
		t.Error("thumbnailAutomatic is false after the pinned file was deleted")
	}

	// The deleted file's sidecar goes with it. A leaked sidecar would be
	// invisible to every other assertion here and accumulate forever.
	final, sidecars, temp := blobs(t, dir)
	if len(final) != 1 || len(sidecars) != 1 || len(temp) != 0 {
		t.Errorf("dir has %d blobs, %d sidecars, %d temps; want 1, 1, 0", len(final), len(sidecars), len(temp))
	}

	// And the grid agrees with the detail screen.
	list := decodeList(t, mustGet(t, c, "/api/models")).Items
	if len(list) != 1 || list[0].ThumbnailFileID == nil || *list[0].ThumbnailFileID != png.ID {
		t.Errorf("grid shows %v, want the PNG (%d)", list, png.ID)
	}
}

// Deleting the whole model takes its sidecars with it. Without this the only
// thing noticing an orphaned thumbnail would be a full disk.
func TestDeletingAModelRemovesItsThumbnails(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "sweep@example.com")

	_, body := c.upload("Doomed", map[string]string{
		"a.png":   thumbFixture(t, "render.png"),
		"b.gcode": thumbFixture(t, "orcaslicer_1.5.gcode"),
	})
	m := decodeModel(t, body)

	if final, sidecars, _ := blobs(t, dir); len(final) != 2 || len(sidecars) != 2 {
		t.Fatalf("before delete: %d blobs, %d sidecars; want 2, 2", len(final), len(sidecars))
	}
	if resp, body := c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d", m.ID), ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete model: got %d: %s", resp.StatusCode, body)
	}
	final, sidecars, temp := blobs(t, dir)
	if len(final) != 0 || len(sidecars) != 0 || len(temp) != 0 {
		t.Errorf("after delete: %d blobs, %d sidecars, %d temps; want all zero (%v)", len(final), len(sidecars), len(temp), sidecars)
	}
}

// The grid resolves thumbnails for every tile in one pass, and a model with
// nothing to show says so rather than borrowing its neighbour's. Two models in
// one list is the minimum that can catch a query which resolves the first row
// and copies it down.
func TestListResolvesThumbnailsPerModel(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "grid@example.com")

	_, body := c.upload("Has one", map[string]string{"a.png": thumbFixture(t, "render.png")})
	withThumb := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)))
	png := fileNamed(t, withThumb, "a.png")

	if _, body := c.upload("Has none", map[string]string{"b.stl": "solid benchy"}); body == "" {
		t.Fatal("second upload returned nothing")
	}

	list := decodeList(t, mustGet(t, c, "/api/models")).Items
	if len(list) != 2 {
		t.Fatalf("got %d models, want 2", len(list))
	}
	for _, m := range list {
		switch m.Name {
		case "Has one":
			if m.ThumbnailFileID == nil || *m.ThumbnailFileID != png.ID {
				t.Errorf("%q: thumbnail = %v, want %d", m.Name, m.ThumbnailFileID, png.ID)
			}
		case "Has none":
			if m.ThumbnailFileID != nil {
				t.Errorf("%q: thumbnail = %v, want nil; an STL-only model has nothing to show", m.Name, m.ThumbnailFileID)
			}
		}
	}
}

// A thumbnail belongs to its owner. Serving one to a stranger would leak a
// picture of somebody else's print, which is the whole content of the file.
func TestThumbnailRequiresOwnership(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	owner := signIn(t, ts, "owner-t@example.com")

	_, body := owner.upload("Private", map[string]string{"a.png": thumbFixture(t, "render.png")})
	m := decodeModel(t, mustGet(t, owner, fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)))
	png := fileNamed(t, m, "a.png")
	path := fmt.Sprintf("/api/models/%d/files/%d/thumbnail", m.ID, png.ID)

	stranger := signIn(t, ts, "stranger-t@example.com")
	if resp, _ := stranger.get(path); resp.StatusCode != http.StatusNotFound {
		t.Errorf("stranger got %d, want 404", resp.StatusCode)
	}

	anon := &client{t: t, ts: ts, hc: &http.Client{}}
	if resp, _ := anon.get(path); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("signed out got %d, want 401", resp.StatusCode)
	}
}

// A file this app cannot make a picture of still uploads. This is the property
// the whole extraction path is arranged around, so it is asserted rather than
// assumed: a G-code file whose thumbnail is behind the read window, an image in
// a format Go cannot decode, and bytes that are not an image at all.
func TestUnextractableFilesStillUpload(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "degrade@example.com")

	resp, body := c.upload("Awkward", map[string]string{
		"a.gcode": thumbFixture(t, "prusaslicer_2.9_qoi.gcode"),
		"b.svg":   `<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`,
		"c.png":   "this is not a PNG at all",
		"d.3mf":   thumbFixture(t, "nothumb.3mf"),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d, want 201: %s", resp.StatusCode, body)
	}
	m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)))

	if len(m.Files) != 4 {
		t.Fatalf("got %d files, want 4", len(m.Files))
	}
	for _, f := range m.Files {
		if f.HasThumbnail {
			t.Errorf("%s claims a thumbnail it should not have", f.Filename)
		}
	}
	if m.ThumbnailFileID != nil {
		t.Errorf("model has a thumbnail = %v, want nil", m.ThumbnailFileID)
	}
	final, sidecars, temp := blobs(t, dir)
	if len(final) != 4 || len(sidecars) != 0 || len(temp) != 0 {
		t.Errorf("dir has %d blobs, %d sidecars, %d temps; want 4, 0, 0", len(final), len(sidecars), len(temp))
	}
}

// The 16 KB window survives the round trip. The fixture's larger 400x300 block
// straddles the edge in the real file, so a server that read the whole thing
// would serve 400x300 here and this is the only place that would notice.
func TestGCodeThumbnailHonoursTheReadWindow(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "window@example.com")

	// The fixture is elided down to 28 KB for the repo, which is under the
	// 144 KB at which the two windows collapse into one read - so it is padded
	// back out with comment lines to restore the gap the real file has.
	raw := thumbFixture(t, "prusaslicer_2.4.gcode")
	head, tail, ok := strings.Cut(raw, "; ---- body elided for the test fixture ----")
	if !ok {
		t.Fatal("fixture has no elision marker")
	}
	padded := head + strings.Repeat("; padding to restore the real file's length\n", 4000) + tail

	_, body := c.upload("Windowed", map[string]string{"a.gcode": padded})
	m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)))
	f := fileNamed(t, m, "a.gcode")
	if !f.HasThumbnail {
		t.Fatal("no thumbnail extracted from a PrusaSlicer file")
	}

	w, h := fetchPNG(t, c, fmt.Sprintf("/api/models/%d/files/%d/thumbnail", m.ID, f.ID))
	if w != 64 || h != 64 {
		t.Errorf("served %dx%d, want 64x64: the 400x300 block starts inside the window and ends past it", w, h)
	}
}

// A sidecar that is not on disk answers 404.
//
// The weaker version of this test asserts only that the request fails; this one
// pins the status, because the difference between 404 and 500 here is a claim
// about what a thumbnail is. It is derived from a blob that is still there, so
// it can be made again by re-uploading the file, and a grid of twenty tiles
// must not answer twenty 500s because one derived PNG went missing.
//
// The honest cost: the row still says has_thumbnail, so it disagrees with the
// disk until the file is uploaded again, and resolution keeps choosing it over
// another file that does have one. That is accepted rather than repaired,
// because the only way to get here is to delete out of UPLOAD_DIR behind the
// app's back - its own delete path removes both - and repairing it would mean
// a GET that writes.
func TestMissingSidecarIsNotFound(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "missing@example.com")

	_, body := c.upload("Broken", map[string]string{"a.png": thumbFixture(t, "render.png")})
	m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)))
	f := fileNamed(t, m, "a.png")

	_, sidecars, _ := blobs(t, dir)
	if len(sidecars) != 1 {
		t.Fatalf("got %d sidecars, want 1", len(sidecars))
	}
	if err := os.Remove(filepath.Join(dir, sidecars[0])); err != nil {
		t.Fatal(err)
	}

	resp, respBody := c.get(fmt.Sprintf("/api/models/%d/files/%d/thumbnail", m.ID, f.ID))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", resp.StatusCode, respBody)
	}
	// huma renders err.Error() into errors[].message even when detail is
	// generic, so nothing that names the storage key or the absolute upload
	// path may reach the client on any error path.
	if strings.Contains(respBody, dir) || strings.Contains(respBody, ".thumb") {
		t.Errorf("body leaks the storage path: %s", respBody)
	}

	// The file itself is untouched: only the derived picture went missing.
	if resp, _ := c.get(fmt.Sprintf("/api/models/%d/files/%d", m.ID, f.ID)); resp.StatusCode != http.StatusOK {
		t.Errorf("downloading the file got %d, want 200: a missing thumbnail is not a missing blob", resp.StatusCode)
	}
}

// Stored thumbnails are bounded, whatever was uploaded. A 4000x3000 photo off a
// phone would otherwise be re-served in full to a 168 px tile.
func TestLargeImagesAreScaledDown(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "big@example.com")

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 2000, 1000))); err != nil {
		t.Fatal(err)
	}

	_, body := c.upload("Big", map[string]string{"a.png": buf.String()})
	m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)))
	f := fileNamed(t, m, "a.png")

	w, h := fetchPNG(t, c, fmt.Sprintf("/api/models/%d/files/%d/thumbnail", m.ID, f.ID))
	if w != 512 || h != 256 {
		t.Errorf("served %dx%d, want 512x256", w, h)
	}
}

// Pinning a file that another transaction is in the middle of deleting must be
// a 422, not a 500.
//
// This is the one race in the feature that is actually reachable: the handler
// checks the file exists with an EXISTS subquery, and the foreign key is
// checked afterwards, so between the two the row can go. The window is real
// but tiny, and rather than lock the row for the duration - which would make
// every pin pay for a case that essentially never happens - the resulting
// SQLSTATE is mapped to the same answer the check would have given.
//
// The window is forced open here rather than waited for: an uncommitted DELETE
// holds a lock the foreign key check must take, so the UPDATE parks until the
// delete commits and then fails its recheck. That is the exact sequence the
// mapping exists for, and it is deterministic - the test waits for Postgres to
// report the backend blocked before releasing it.
//
// A weaker version would delete the file first and then pin it, which takes
// the EXISTS branch and returns 404 without ever reaching the constraint.
func TestPinningAFileBeingDeletedIsRefusedNotAnError(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "race@example.com")

	resp, body := c.upload("Raced", map[string]string{
		"a.gcode": thumbFixture(t, "orcaslicer_1.5.gcode"),
		"b.3mf":   thumbFixture(t, "bambustudio.3mf"),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	m := decodeModel(t, body)
	victim := fileNamed(t, m, "b.3mf")

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	var holder int32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&holder); err != nil {
		t.Fatalf("backend pid: %v", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM model_files WHERE id = $1", victim.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	type result struct {
		status int
		body   string
	}
	done := make(chan result, 1)
	go func() {
		resp, body := setThumbnail(t, c, m.ID, &victim.ID)
		done <- result{resp.StatusCode, body}
	}()

	// Wait for the pin to actually be parked on this transaction's lock.
	// Without this the commit can land first and the test would pass through
	// the ordinary already-deleted path, proving nothing. Asking Postgres who
	// is blocking whom rather than matching on query text is what makes that a
	// statement about these two statements and not about whatever else happens
	// to be waiting.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked int
		err := pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE wait_event_type = 'Lock'
			    AND $1 = ANY(pg_blocking_pids(pid))
			    AND query ILIKE '%thumbnail_file_id%'`, holder).Scan(&blocked)
		if err != nil {
			t.Fatalf("pg_stat_activity: %v", err)
		}
		if blocked > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pin never blocked on the delete's lock; the race was not reproduced")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// A timed receive, because setThumbnail reports failures with t.Fatalf and
	// that ends only this goroutine when it runs off the test's own. Without
	// the timeout an assertion failure inside it would hang the package.
	var got result
	select {
	case got = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the pin never returned; setThumbnail gave up inside the goroutine")
	}
	if got.status != http.StatusUnprocessableEntity {
		t.Fatalf("pin during delete: got %d, want 422: %s", got.status, got.body)
	}
	// And it says so in words the user can act on, without naming a column or
	// a constraint.
	if strings.Contains(got.body, "23503") || strings.Contains(got.body, "constraint") {
		t.Errorf("the error body leaks the database's vocabulary: %s", got.body)
	}
}
