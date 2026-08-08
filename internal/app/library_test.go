package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/robert-crandall/go-home-server/db"

	"github.com/robert-crandall/3d-library/internal/library"
)

// Every test here runs against real Postgres and the real router, because the
// things worth checking - that a failed upload leaves no half-written model,
// that one user cannot read another's, that an oversized body is refused before
// it is all read - are properties of the seam between HTTP, the filesystem and
// the database. A service-level test with a fake store would assert none of
// them.

func testDatabase(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	return url
}

// client is a signed-in HTTP client. Sessions are cookie-based, so a cookie jar
// is all it takes to be a distinct user.
type client struct {
	t  *testing.T
	ts *httptest.Server
	hc *http.Client
}

func signIn(t *testing.T, ts *httptest.Server, email string) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	c := &client{t: t, ts: ts, hc: &http.Client{Jar: jar}}

	body := strings.NewReader(fmt.Sprintf(
		`{"email":%q,"password":"correct-horse-battery"}`, email))
	resp, err := c.hc.Post(ts.URL+"/api/auth/register", "application/json", body)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(resp.Body)
		t.Fatalf("register %s: got %d: %s", email, resp.StatusCode, detail)
	}
	return c
}

// upload drives the whole client-side flow for a multi-file model: the first
// file creates the model, each remaining file is its own request. Filenames are
// sorted so the file that creates the model is deterministic.
//
// On success it returns the last upload's response (201) paired with the
// assembled model, re-read so the caller sees every file rather than just the
// one the create call returned. On failure it returns the first failing
// response, which is what the UI would surface.
func (c *client) upload(modelName string, files map[string]string) (*http.Response, string) {
	c.t.Helper()

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	ct, part := filePart(c.t, names[0], files[names[0]])
	resp, body := c.post(modelName, ct, part)
	if resp.StatusCode != http.StatusCreated {
		return resp, body
	}
	model := decodeModel(c.t, body)

	for _, name := range names[1:] {
		ct, part = filePart(c.t, name, files[name])
		resp, body = c.addFile(model.ID, ct, part)
		if resp.StatusCode != http.StatusCreated {
			return resp, body
		}
	}

	_, assembled := c.get(fmt.Sprintf("/api/models/%d", model.ID))
	return resp, assembled
}

// filePart builds a one-file multipart body.
func filePart(t *testing.T, filename, contents string) (string, io.Reader) {
	t.Helper()
	var buf strings.Builder
	mw := multipart.NewWriter(&buf)
	w, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := io.WriteString(w, contents); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return mw.FormDataContentType(), strings.NewReader(buf.String())
}

func (c *client) post(modelName, contentType string, body io.Reader) (*http.Response, string) {
	c.t.Helper()
	return c.do(c.ts.URL+"/api/models?"+url.Values{"name": {modelName}}.Encode(), contentType, body)
}

func (c *client) addFile(modelID int64, contentType string, body io.Reader) (*http.Response, string) {
	c.t.Helper()
	return c.do(fmt.Sprintf("%s/api/models/%d/files", c.ts.URL, modelID), contentType, body)
}

func (c *client) do(url, contentType string, body io.Reader) (*http.Response, string) {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		c.t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("read body: %v", err)
	}
	return resp, string(out)
}

func (c *client) get(path string) (*http.Response, string) {
	c.t.Helper()
	resp, err := c.hc.Get(c.ts.URL + path)
	if err != nil {
		c.t.Fatalf("get %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("read %s: %v", path, err)
	}
	return resp, string(out)
}

func decodeModel(t *testing.T, body string) library.Model {
	t.Helper()
	var m library.Model
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("decode model: %v (body %q)", err, body)
	}
	return m
}

// blobs lists the real (non-temp) files in dir, plus any leftover temp files.
// Both halves matter: a test that only counted blobs would pass while every
// failed upload left a .tmp- file behind forever.
func blobs(t *testing.T, dir string) (final, temp []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			temp = append(temp, e.Name())
		} else {
			final = append(final, e.Name())
		}
	}
	return final, temp
}

// The happy path end to end: upload, then read the same model back out of the
// list and by id. Asserting all three in one test is what makes it a *seam*
// test - checking only the 201 would pass even if nothing were ever committed.
func TestUploadThenBrowse(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "owner@example.com")

	resp, body := c.upload("Benchy", map[string]string{
		"benchy.stl":  "solid benchy",
		"preview.png": "not really a png",
		"notes.txt":   "print at 0.2mm",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d, want 201: %s", resp.StatusCode, body)
	}
	created := decodeModel(t, body)
	if created.Name != "Benchy" || created.FileCount != 3 {
		t.Errorf("created = %+v, want name Benchy with 3 files", created)
	}
	// 12 + 16 + 14. Pinning the number rather than "> 0" is what catches a
	// size that was recorded from the wrong reader.
	if want := int64(len("solid benchy") + len("not really a png") + len("print at 0.2mm")); created.TotalSize != want {
		t.Errorf("totalSize = %d, want %d", created.TotalSize, want)
	}

	final, temp := blobs(t, dir)
	if len(final) != 3 {
		t.Errorf("stored %d blobs, want 3: %v", len(final), final)
	}
	if len(temp) != 0 {
		t.Errorf("left %d temp files behind: %v", len(temp), temp)
	}

	resp, body = c.get("/api/models")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: got %d: %s", resp.StatusCode, body)
	}
	var listed []library.Model
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("list = %+v, want just the model that was created", listed)
	}
	if listed[0].FileCount != 3 || listed[0].TotalSize != created.TotalSize {
		t.Errorf("list entry = %+v, want the same counts as the create response", listed[0])
	}
	// The grid renders from the list, which never needs the file rows, so the
	// list must not pay for them.
	if listed[0].Files != nil {
		t.Errorf("list included per-file detail: %+v", listed[0].Files)
	}

	resp, body = c.get(fmt.Sprintf("/api/models/%d", created.ID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: got %d: %s", resp.StatusCode, body)
	}
	got := decodeModel(t, body)
	if len(got.Files) != 3 {
		t.Fatalf("get returned %d files, want 3", len(got.Files))
	}
	types := map[string]string{}
	for _, f := range got.Files {
		types[f.Filename] = f.Type
	}
	for filename, want := range map[string]string{
		"benchy.stl": "stl", "preview.png": "image", "notes.txt": "document",
	} {
		if types[filename] != want {
			t.Errorf("%s: type = %q, want %q", filename, types[filename], want)
		}
	}
}

// An empty library must encode as [] and not null. Go marshals a nil slice as
// null, and the SPA calls .length on the result, so this is one nil away from a
// blank page on the exact screen a new user sees first.
func TestEmptyLibraryIsAnEmptyArray(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "empty@example.com")

	resp, body := c.get("/api/models")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: got %d: %s", resp.StatusCode, body)
	}
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("body = %q, want []", body)
	}
}

// One user must not see or fetch another's models, and the refusal must be a
// 404 rather than a 403 - a 403 confirms that a model exists at that id.
func TestModelsAreScopedToTheirOwner(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})

	owner := signIn(t, ts, "owner@example.com")
	resp, body := owner.upload("Private", map[string]string{"a.stl": "solid"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	created := decodeModel(t, body)

	other := signIn(t, ts, "other@example.com")
	if resp, body := other.get("/api/models"); strings.TrimSpace(body) != "[]" {
		t.Errorf("other user's list = %q (status %d), want []", body, resp.StatusCode)
	}
	resp, body = other.get(fmt.Sprintf("/api/models/%d", created.ID))
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("other user's get: got %d, want 404: %s", resp.StatusCode, body)
	}
}

// Anonymous callers get 401 on every route. The upload check is the one that
// matters most: it lives in middleware precisely so an anonymous caller cannot
// stream gigabytes at the disk before being refused.
func TestLibraryRequiresAuthentication(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})

	anon := &client{t: t, ts: ts, hc: &http.Client{}}
	for _, tc := range []struct {
		name string
		do   func() (*http.Response, string)
	}{
		{"list", func() (*http.Response, string) { return anon.get("/api/models") }},
		{"get", func() (*http.Response, string) { return anon.get("/api/models/1") }},
		{"upload", func() (*http.Response, string) {
			return anon.upload("Sneaky", map[string]string{"a.stl": "solid"})
		}},
	} {
		if resp, body := tc.do(); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: got %d, want 401: %s", tc.name, resp.StatusCode, body)
		}
	}

	final, temp := blobs(t, dir)
	if len(final) != 0 || len(temp) != 0 {
		t.Errorf("anonymous upload wrote to disk: %v %v", final, temp)
	}
}

// A file over the per-file cap is refused, and - the part that matters - the
// bytes already staged are removed. Without the cleanup, every rejected upload
// would leave most of a large file on disk forever.
func TestOversizedFileIsRejectedAndCleanedUp(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir, MaxFileBytes: 1024})
	c := signIn(t, ts, "big@example.com")

	resp, body := c.upload("Too big", map[string]string{
		"huge.stl": strings.Repeat("x", 4096),
	})
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413: %s", resp.StatusCode, body)
	}

	final, temp := blobs(t, dir)
	if len(final) != 0 || len(temp) != 0 {
		t.Errorf("rejected upload left files behind: final=%v temp=%v", final, temp)
	}
	if resp, body := c.get("/api/models"); strings.TrimSpace(body) != "[]" {
		t.Errorf("rejected upload created a model: %q (status %d)", body, resp.StatusCode)
	}
}

// A file exactly at the cap is accepted. Paired with the test above this pins
// the boundary: a >= instead of > would pass one of these two and fail the other.
func TestFileExactlyAtTheCapIsAccepted(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir(), MaxFileBytes: 1024})
	c := signIn(t, ts, "exact@example.com")

	resp, body := c.upload("Exactly", map[string]string{
		"exact.stl": strings.Repeat("x", 1024),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", resp.StatusCode, body)
	}
	if m := decodeModel(t, body); m.TotalSize != 1024 {
		t.Errorf("totalSize = %d, want 1024", m.TotalSize)
	}
}

// The 21st file is refused before its body is read, and refusing it leaves no
// new blob. The files already in the model stay, because they are committed
// rows - the cap bounds a model, not a request.
func TestTooManyFilesIsRejectedAndCleanedUp(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir, MaxFiles: 3})
	c := signIn(t, ts, "many@example.com")

	files := map[string]string{}
	for i := range 3 {
		files[fmt.Sprintf("part%d.stl", i)] = "solid"
	}
	resp, body := c.upload("Too many", files)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("filling the model: got %d, want 201: %s", resp.StatusCode, body)
	}
	full := decodeModel(t, body)

	before, _ := blobs(t, dir)
	ct, part := filePart(t, "one-too-many.stl", "solid")
	resp, body = c.addFile(full.ID, ct, part)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422: %s", resp.StatusCode, body)
	}

	final, temp := blobs(t, dir)
	if len(final) != len(before) || len(temp) != 0 {
		t.Errorf("the refused file left something behind: final=%v temp=%v", final, temp)
	}
	if m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", full.ID))); m.FileCount != 3 {
		t.Errorf("fileCount = %d, want 3", m.FileCount)
	}
}

func mustGet(t *testing.T, c *client, path string) string {
	t.Helper()
	resp, body := c.get(path)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get %s: got %d: %s", path, resp.StatusCode, body)
	}
	return body
}

// Exactly at the file-count cap is accepted, pinning the other side of the same
// boundary.
func TestExactlyMaxFilesIsAccepted(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir(), MaxFiles: 3})
	c := signIn(t, ts, "atcap@example.com")

	files := map[string]string{}
	for i := range 3 {
		files[fmt.Sprintf("part%d.stl", i)] = "solid"
	}
	resp, body := c.upload("At the cap", files)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", resp.StatusCode, body)
	}
	if m := decodeModel(t, body); m.FileCount != 3 {
		t.Errorf("fileCount = %d, want 3", m.FileCount)
	}
}

// Parts that are not files in the "file" field are refused rather than stored.
// A text field carries the same field name with no filename, so checking only
// the field name would store a bogus zero-byte "file" for every stray input a
// future form grows.
func TestNonFilePartsAreRejected(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "parts@example.com")

	for _, tc := range []struct {
		name  string
		write func(*multipart.Writer)
	}{
		{"wrong field name", func(mw *multipart.Writer) {
			w, _ := mw.CreateFormFile("attachments", "a.stl")
			io.WriteString(w, "solid")
		}},
		{"text field, no filename", func(mw *multipart.Writer) {
			w, _ := mw.CreateFormField("file")
			io.WriteString(w, "solid")
		}},
		{"no parts at all", func(*multipart.Writer) {}},
		{"two files in one request", func(mw *multipart.Writer) {
			for _, name := range []string{"a.stl", "b.stl"} {
				w, _ := mw.CreateFormFile("file", name)
				io.WriteString(w, "solid")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf strings.Builder
			mw := multipart.NewWriter(&buf)
			tc.write(mw)
			if err := mw.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			resp, body := c.post("Bad", mw.FormDataContentType(), strings.NewReader(buf.String()))
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("got %d, want 422: %s", resp.StatusCode, body)
			}
			if final, temp := blobs(t, dir); len(final) != 0 || len(temp) != 0 {
				t.Errorf("wrote files anyway: final=%v temp=%v", final, temp)
			}
		})
	}

	if resp, body := c.get("/api/models"); strings.TrimSpace(body) != "[]" {
		t.Errorf("a rejected upload created a model: %q (status %d)", body, resp.StatusCode)
	}
}

// A multi-file upload that fails partway keeps what already committed and
// leaves no trace of what failed. Because one request carries one file, the
// files that already succeeded are real committed rows and it would be wrong to
// discard them; what must not happen is a blob for the file that was refused.
//
// The model itself is created by its *first* file, so a first-file failure
// leaves no model at all - that case is asserted separately below.
func TestPartialUploadKeepsWhatCommittedAndNothingElse(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir, MaxFileBytes: 1024})
	c := signIn(t, ts, "partial@example.com")

	ct, part := filePart(t, "good.stl", "solid")
	resp, body := c.post("Half", ct, part)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first file: got %d, want 201: %s", resp.StatusCode, body)
	}
	m := decodeModel(t, body)

	ct, part = filePart(t, "huge.stl", strings.Repeat("x", 4096))
	resp, body = c.addFile(m.ID, ct, part)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413: %s", resp.StatusCode, body)
	}

	final, temp := blobs(t, dir)
	if len(final) != 1 || len(temp) != 0 {
		t.Errorf("want exactly the one committed blob: final=%v temp=%v", final, temp)
	}
	got := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", m.ID)))
	if got.FileCount != 1 || got.Files[0].Filename != "good.stl" {
		t.Errorf("model = %+v, want just good.stl", got)
	}
}

// A model is only ever created by a file that committed. If the very first file
// fails there must be no model row, because milestone 1 has no delete and an
// empty model would sit in the grid forever.
func TestFailedFirstFileCreatesNoModel(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir, MaxFileBytes: 1024})
	c := signIn(t, ts, "firstfail@example.com")

	ct, part := filePart(t, "huge.stl", strings.Repeat("x", 4096))
	resp, body := c.post("Never born", ct, part)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413: %s", resp.StatusCode, body)
	}
	if final, temp := blobs(t, dir); len(final) != 0 || len(temp) != 0 {
		t.Errorf("left files behind: final=%v temp=%v", final, temp)
	}
	if strings.TrimSpace(mustGet(t, c, "/api/models")) != "[]" {
		t.Error("a failed first file created a model")
	}
}

// Adding a file to somebody else's model is a 404, and it must not cost the
// attacker's target any disk.
func TestAddFileToAnotherUsersModelIsNotFound(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})

	owner := signIn(t, ts, "owner2@example.com")
	resp, body := owner.upload("Mine", map[string]string{"a.stl": "solid"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("owner upload: got %d: %s", resp.StatusCode, body)
	}
	mine := decodeModel(t, body)

	before, _ := blobs(t, dir)
	intruder := signIn(t, ts, "intruder2@example.com")
	ct, part := filePart(t, "theirs.stl", "solid")
	resp, body = intruder.addFile(mine.ID, ct, part)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", resp.StatusCode, body)
	}
	if final, temp := blobs(t, dir); len(final) != len(before) || len(temp) != 0 {
		t.Errorf("the refused upload left files behind: final=%v temp=%v", final, temp)
	}
}

// A body over the whole-request cap is refused *while it is still arriving*,
// not after it has all been read.
//
// This is the one guard nothing else covers: mime/multipart discards
// arbitrarily many preamble lines before yielding the first part, so without a
// total cap neither the per-file limit nor the file count ever engages and a
// client can stream forever. The body here is entirely preamble for that
// reason. The reader counts what is actually consumed; the assertion is "well
// under the whole body" rather than an exact number, because some bytes sit in
// the socket buffer before the 413 makes it back.
func TestOversizedBodyIsRejectedWhileStillStreaming(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	// 2 files x 1 KB + 1 MB of slop, so the cap is about 1 MB.
	ts := newTestServer(t, pool, library.Options{Dir: dir, MaxFileBytes: 1024, MaxFiles: 2})
	c := signIn(t, ts, "flood@example.com")

	const total = 64 << 20
	body := &countingReader{remaining: total}
	resp, out := c.post("Flood", "multipart/form-data; boundary=zzz", body)

	// A connection reset is also a pass: MaxBytesReader closes the connection
	// once the limit is hit, and whether the 413 makes it back first is a race.
	if resp != nil && resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("got %d, want 413: %s", resp.StatusCode, out)
	}
	if body.read > total/2 {
		t.Errorf("server read %d of %d bytes - the body cap is not engaging", body.read, total)
	}
	if final, temp := blobs(t, dir); len(final) != 0 || len(temp) != 0 {
		t.Errorf("rejected body left files behind: final=%v temp=%v", final, temp)
	}
}

// countingReader streams `remaining` bytes of multipart preamble, counting what
// is actually consumed.
type countingReader struct {
	remaining int
	read      int
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	// A newline every 64 bytes. Without them this is one enormous line and
	// bufio's own buffer fills first, which fails the request for a reason that
	// has nothing to do with the guard under test.
	for i := range n {
		if i%64 == 63 {
			p[i] = '\n'
		} else {
			p[i] = 'x'
		}
	}
	r.remaining -= n
	r.read += n
	return n, nil
}

// A database failure after the blobs are on disk must not leave a model behind,
// and - because the commit was never attempted - must not leave blobs either.
// The closed pool stands in for any failure before COMMIT.
//
// The asymmetry is deliberate and is what this test pins: before COMMIT is
// attempted, cleanup is safe and required. After it, no file is ever removed,
// because a commit can succeed on the server and still return a network error,
// and deleting the blobs of a row that does exist would manufacture exactly the
// dangling entry the ordering exists to prevent.
func TestDatabaseFailureBeforeCommitLeavesNothing(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()

	// A second pool, used only by the library, so closing it fails the insert
	// without also breaking the session lookup that authenticates the request.
	libPool, err := db.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	ts := newTestServerWithLibraryPool(t, pool, libPool, library.Options{Dir: dir})
	c := signIn(t, ts, "dbdown@example.com")
	libPool.Close()

	resp, body := c.upload("Doomed", map[string]string{"a.stl": "solid"})
	if resp.StatusCode < 500 {
		t.Fatalf("got %d, want a 5xx: %s", resp.StatusCode, body)
	}
	final, temp := blobs(t, dir)
	if len(final) != 0 || len(temp) != 0 {
		t.Errorf("failed insert left files behind: final=%v temp=%v", final, temp)
	}
}

// A client filename can never contribute a path segment. The stored blob name
// is random, and the display name is reduced to a base name on both separators
// - filepath.Base is a no-op on backslashes when the server runs on Linux,
// which is exactly the case a Windows-flavoured payload would exploit.
func TestHostileFilenamesAreNeutralized(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "hostile@example.com")

	resp, body := c.upload("Hostile", map[string]string{
		`../../escape.stl`:                "solid",
		`..\..\windows-escape.stl`:        "solid",
		strings.Repeat("n", 400) + ".stl": "solid",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", resp.StatusCode, body)
	}
	created := decodeModel(t, body)

	// Nothing outside the upload directory.
	if entries, err := os.ReadDir(filepath.Dir(dir)); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".stl") {
				t.Errorf("a blob escaped the upload directory: %s", e.Name())
			}
		}
	}
	final, _ := blobs(t, dir)
	if len(final) != 3 {
		t.Fatalf("stored %d blobs, want 3: %v", len(final), final)
	}
	for _, name := range final {
		if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
			t.Errorf("storage key %q contains a path segment", name)
		}
	}

	_, body = c.get(fmt.Sprintf("/api/models/%d", created.ID))
	for _, f := range decodeModel(t, body).Files {
		if strings.ContainsAny(f.Filename, `/\`) {
			t.Errorf("stored display name %q still contains a separator", f.Filename)
		}
		if len(f.Filename) > 255 {
			t.Errorf("stored display name is %d bytes, want <= 255", len(f.Filename))
		}
	}
}

// The production caps are the numbers the epic settled. Without this the
// Options defaults could drift to anything and every other test would still
// pass, because they all pass their own caps in.
func TestProductionCapsAreTheAgreedNumbers(t *testing.T) {
	if library.MaxFileBytes != 500<<20 {
		t.Errorf("MaxFileBytes = %d, want 500 MB", library.MaxFileBytes)
	}
	if library.MaxFiles != 20 {
		t.Errorf("MaxFiles = %d, want 20", library.MaxFiles)
	}
}

// NewService refuses a directory it cannot use, rather than discovering it at
// the first upload - by which point a user is watching a spinner.
func TestNewServiceRejectsAnUnusableDirectory(t *testing.T) {
	for _, tc := range []struct{ name, dir string }{
		{"empty", ""},
		{"missing", filepath.Join(t.TempDir(), "nope")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := library.NewService(nil, library.Options{Dir: tc.dir}); err == nil {
				t.Error("got nil error, want a refusal")
			}
		})
	}

	// A file is not a directory. Worth its own case: os.Stat succeeds, so only
	// the IsDir check catches it.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := library.NewService(nil, library.Options{Dir: f}); err == nil {
		t.Error("a plain file was accepted as an upload directory")
	}
}
