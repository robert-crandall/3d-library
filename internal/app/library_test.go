package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

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

// send is get's mutating sibling: any method, an optional JSON body, and the
// response read to completion so the caller can assert on it.
func (c *client) send(method, path, body string) (*http.Response, string) {
	c.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, c.ts.URL+path, r)
	if err != nil {
		c.t.Fatalf("request %s %s: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("read %s %s: %v", method, path, err)
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

// decodeModel reads a single-model response. Every endpoint that returns one
// whole model - create, get, update - returns the detail shape; only the list
// returns the summary.
func decodeModel(t *testing.T, body string) library.ModelDetail {
	t.Helper()
	var m library.ModelDetail
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

// waitFor polls until cond holds, so a test can wait for the thing it actually
// cares about instead of guessing a sleep long enough to cover a slow machine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
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
	// The grid renders from the list, which needs neither the file rows nor the
	// prose, so the list must not pay for them. Checked against the raw JSON
	// rather than the decoded struct: library.Model has no such fields, so
	// decoding would drop them silently and this would pass no matter what the
	// server sent.
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode list keys: %v", err)
	}
	for _, key := range []string{"files", "description", "printTips", "sourceUrl"} {
		if _, ok := raw[0][key]; ok {
			t.Errorf("list entry carries %q, which the grid does not render", key)
		}
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

// The boundary parameter is not what makes a body multipart. Reading the media
// type and not just the parameters is the difference between rejecting a
// mislabelled body up front and handing it to a parser that will find no parts
// and say something less useful.
func TestUploadRefusesANonMultipartContentType(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "mislabelled@example.com")

	contentType, body := filePart(t, "a.stl", "solid")
	_, boundary, _ := strings.Cut(contentType, "boundary=")

	resp, out := c.post("Mislabelled", "text/plain; boundary="+boundary, body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422: %s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "multipart/form-data") {
		t.Errorf("the error does not say what was expected: %s", out)
	}
	if final, temp := blobs(t, dir); len(final) != 0 || len(temp) != 0 {
		t.Errorf("wrote files anyway: final=%v temp=%v", final, temp)
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

// Two uploads racing for the last slot produce one file, not two.
//
// The count that guards the cap once ran outside the transaction, on a separate
// connection, before the body was read - so two requests that both saw "2 of 3"
// both passed it and the model ended up with 4 files. A cap that only holds
// when nobody is in a hurry is not a cap.
//
// The race is made deterministic rather than hoped for: this test takes the
// model's row lock itself and holds it until both requests are demonstrably
// blocked on it, which is the interleaving that used to break. Sleeping instead
// would pass on a fast machine whether or not the bug was fixed.
func TestRacingUploadsCannotExceedMaxFiles(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir, MaxFiles: 3})
	c := signIn(t, ts, "race@example.com")

	resp, body := c.upload("Racy", map[string]string{"a.stl": "solid", "b.stl": "solid"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seeding the model: got %d, want 201: %s", resp.StatusCode, body)
	}
	model := decodeModel(t, body)

	ctx := context.Background()
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer blocker.Rollback(ctx)
	var locked int64
	if err := blocker.QueryRow(ctx,
		"SELECT id FROM models WHERE id = $1 FOR UPDATE", model.ID).Scan(&locked); err != nil {
		t.Fatalf("lock the model row: %v", err)
	}
	var blockerPID int32
	if err := blocker.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&blockerPID); err != nil {
		t.Fatalf("read the blocker's pid: %v", err)
	}

	codes := make(chan int, 2)
	for i := range 2 {
		go func() {
			ct, part := filePart(t, fmt.Sprintf("race%d.stl", i), "solid")
			resp, _ := c.addFile(model.ID, ct, part)
			codes <- resp.StatusCode
		}()
	}

	// Both requests have to be *waiting on this lock* before it is released.
	// Releasing early would let them serialise naturally and the test would
	// prove nothing.
	waitForBlockedBackends(t, dbURL, blockerPID, 2)
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release the lock: %v", err)
	}

	created, refused := 0, 0
	for range 2 {
		switch code := <-codes; code {
		case http.StatusCreated:
			created++
		case http.StatusUnprocessableEntity:
			refused++
		default:
			t.Errorf("unexpected status %d", code)
		}
	}
	if created != 1 || refused != 1 {
		t.Errorf("got %d created and %d refused, want exactly one of each", created, refused)
	}

	if m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", model.ID))); m.FileCount != 3 {
		t.Errorf("fileCount = %d, want 3 - the cap did not hold under concurrency", m.FileCount)
	}
	// The loser's bytes must not be left behind either: it staged a blob before
	// it ever reached the lock.
	final, temp := blobs(t, dir)
	if len(final) != 3 || len(temp) != 0 {
		t.Errorf("got %d blobs and %d temp files, want 3 and 0", len(final), len(temp))
	}
}

// waitForBlockedBackends waits until `want` backends are stuck behind
// `blocker`. It polls rather than sleeps so the test is not tuned to any
// particular machine, and it asks about this lock in particular rather than
// counting every lock wait in the database - a wait on something unrelated
// would otherwise satisfy it and release the lock before the requests had
// reached it, which is exactly the interleaving the pre-fix code survives.
//
// The walk is recursive because row-lock queues are: only the first waiter
// blocks on `blocker`, and the second blocks on the first. pg_blocking_pids
// reports direct blockers only, so asking who is blocked *by the blocker*
// finds one backend and never two, however long you wait for it.
func waitForBlockedBackends(t *testing.T, dbURL string, blocker int32, want int) {
	t.Helper()

	ctx := context.Background()
	watcher, err := db.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("watcher connect: %v", err)
	}
	defer watcher.Close()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked int
		if err := watcher.QueryRow(ctx,
			`WITH RECURSIVE queued(pid) AS (
			     SELECT pid FROM pg_stat_activity
			      WHERE datname = current_database()
			        AND $1 = ANY(pg_blocking_pids(pid))
			   UNION
			     SELECT a.pid FROM pg_stat_activity a, queued q
			      WHERE a.datname = current_database()
			        AND q.pid = ANY(pg_blocking_pids(a.pid))
			 )
			 SELECT count(*) FROM queued`, blocker).Scan(&blocked); err != nil {
			t.Fatalf("inspect pg_stat_activity: %v", err)
		}
		if blocked >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fewer than %d backends ever blocked behind the test's lock on the model row", want)
}

// A client that gives up mid-upload leaves nothing behind.
//
// This is the load-bearing fact under the UI's handling of an ambiguous
// failure. When `fetch` rejects, the browser cannot know what the server did,
// and if the server could still go on to commit then the UI's confirming
// re-read could look before the model existed and wrongly conclude it is safe
// to upload again - which, in a milestone with no delete, means a duplicate
// nobody can remove.
//
// It does not, and the reason is simpler than context propagation: `fetch`
// rejects when the connection fails before the response arrives, and if the
// connection failed early enough for that, the body never finished arriving
// either. The upload fails while reading it, long before COMMIT. (Verified by
// mutation: the test still passes with the request context replaced by an
// uncancellable one, so it is the truncated body doing the work, not
// cancellation.)
//
// That leaves one interleaving this does NOT cover: the body arrives complete,
// the server commits, and the *response* is lost. Then the model exists and
// the confirming re-read finds it, which is the case the re-read is for. The
// genuinely undecidable sliver - connection lost while COMMIT itself is in
// flight - is microseconds wide on the server against a human clicking a
// button, and the service already reports it as a 500. M2 brings delete, which
// is what makes even that repairable.
//
// The temp-file half of this is load-bearing too: an upload that dies partway
// through staging must not leave its bytes on disk.
func TestAbandonedUploadCommitsNothing(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "quitter@example.com")

	// A body that is deliberately never finished: the multipart part is opened
	// and some bytes are written, then the request is abandoned. The server is
	// blocked reading the rest when the connection goes.
	pr, pw := io.Pipe()
	form := multipart.NewWriter(pw)
	ctx, abandon := context.WithCancel(context.Background())
	// Also on the failure paths below: if the staging poll times out, both
	// goroutines are still parked on the pipe, and the temp dir cleanup would
	// wait for them.
	defer abandon()

	go func() {
		part, err := form.CreateFormFile("file", "half.stl")
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		part.Write([]byte("solid partial"))
		// Hand back to the test, which cancels. The pipe write below blocks
		// until then, so the request really is in flight and incomplete.
		<-ctx.Done()
		pw.CloseWithError(errors.New("client gave up"))
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ts.URL+"/api/models?"+url.Values{"name": {"Abandoned"}}.Encode(), pr)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())

	errc := make(chan error, 1)
	go func() {
		resp, err := c.hc.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		errc <- err
	}()

	// Wait for the server to actually stage the upload before pulling the plug.
	// A fixed sleep here would let the test pass without the server having
	// created anything at all, which proves nothing: of course an upload that
	// never started committed nothing.
	waitFor(t, "the server to stage the partial upload", func() bool {
		_, temp := blobs(t, dir)
		return len(temp) == 1
	})
	abandon()
	if err := <-errc; err == nil {
		t.Fatal("the abandoned request somehow succeeded")
	}

	// The client sees the connection go before the handler has finished
	// unwinding, so poll for the cleanup rather than sleeping past it.
	waitFor(t, "the server to remove the staged file", func() bool {
		final, temp := blobs(t, dir)
		return len(final) == 0 && len(temp) == 0
	})

	var models []library.Model
	if err := json.Unmarshal([]byte(mustGet(t, c, "/api/models")), &models); err != nil {
		t.Fatalf("decode library: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("got %d models, want 0 - an abandoned upload committed", len(models))
	}
}

// ---------------------------------------------------------------------------
// Milestone 2: inspect, edit, delete.
// ---------------------------------------------------------------------------

// edits is the whole PUT body. Every field is required, because the update
// replaces the editable metadata rather than patching it.
func edits(name, description, printTips, sourceURL string) string {
	body, err := json.Marshal(map[string]string{
		"name": name, "description": description,
		"printTips": printTips, "sourceUrl": sourceURL,
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

// An edit is only real if it survives the round trip. Asserting on the PUT
// response alone would pass even if the handler never wrote anything, so this
// re-reads through a fresh GET - which is also what the page does not do, on
// purpose, so this is the only thing checking the two agree.
func TestEditingAModelPersists(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "editor@example.com")

	resp, body := c.upload("Draft", map[string]string{"a.stl": "solid a"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	created := decodeModel(t, body)
	if created.Description != "" || created.PrintTips != "" || created.SourceURL != "" {
		t.Errorf("a new model starts with metadata: %+v", created)
	}

	resp, body = c.send(http.MethodPut, fmt.Sprintf("/api/models/%d", created.ID),
		edits("Filament Dry Box", "Holds four spools.", "PETG at 245 C.\nBed 80 C.",
			"https://www.printables.com/model/48213"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: got %d: %s", resp.StatusCode, body)
	}

	// Both the response and a fresh read, because they are different claims:
	// the first is what the page renders, the second is what was stored.
	for _, got := range []library.ModelDetail{
		decodeModel(t, body),
		decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", created.ID))),
	} {
		if got.Name != "Filament Dry Box" {
			t.Errorf("name = %q", got.Name)
		}
		if got.Description != "Holds four spools." {
			t.Errorf("description = %q", got.Description)
		}
		if got.PrintTips != "PETG at 245 C.\nBed 80 C." {
			t.Errorf("printTips = %q", got.PrintTips)
		}
		if got.SourceURL != "https://www.printables.com/model/48213" {
			t.Errorf("sourceUrl = %q", got.SourceURL)
		}
		// An update must not disturb the files it does not mention.
		if got.FileCount != 1 || len(got.Files) != 1 || got.Files[0].Filename != "a.stl" {
			t.Errorf("update changed the files: %+v", got.Files)
		}
	}
}

// The two rules the server owns. Both are refusals, and both have to leave the
// stored row exactly as it was - a validator that rejects the request after
// writing half of it is worse than no validator.
func TestUpdateRefusesWhatItCannotStore(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "refuser@example.com")

	resp, body := c.upload("Keeper", map[string]string{"a.stl": "solid a"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	id := decodeModel(t, body).ID
	path := fmt.Sprintf("/api/models/%d", id)

	for _, tc := range []struct {
		what string
		body string
	}{
		{"an empty name", edits("", "", "", "")},
		{"a whitespace-only name", edits("   \t ", "", "", "")},
		// A name that is only whitespace is the interesting one: huma's
		// minLength sees three characters and lets it through, so nothing but
		// the service's own trim stands between it and a nameless model.
		{"a javascript: source", edits("Keeper", "", "", "javascript:alert(1)")},
		{"an ftp: source", edits("Keeper", "", "", "ftp://example.com/x.stl")},
		{"a source with no host", edits("Keeper", "", "", "https:garbage")},
		{"a source that is just words", edits("Keeper", "", "", "printables, probably")},
	} {
		resp, out := c.send(http.MethodPut, path, tc.body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422: %s", tc.what, resp.StatusCode, out)
		}
	}

	got := decodeModel(t, mustGet(t, c, path))
	if got.Name != "Keeper" || got.SourceURL != "" {
		t.Errorf("a refused update still changed the model: %+v", got)
	}
}

// The shapes a source URL is allowed to be. Paired with the refusals above so
// the validator cannot pass both halves by rejecting everything.
func TestUpdateAcceptsUsableSourceURLs(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "linker@example.com")

	resp, body := c.upload("Linked", map[string]string{"a.stl": "solid a"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	path := fmt.Sprintf("/api/models/%d", decodeModel(t, body).ID)

	for _, want := range []string{
		"",
		"http://example.com",
		"https://www.printables.com/model/48213-filament-dry-box",
		"https://example.com/x?y=1#z",
	} {
		resp, out := c.send(http.MethodPut, path, edits("Linked", "", "", want))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%q: got %d, want 200: %s", want, resp.StatusCode, out)
		}
		if got := decodeModel(t, out).SourceURL; got != want {
			t.Errorf("stored %q, want %q", got, want)
		}
	}
}

// Adding files to a model that already exists - the thing milestone 1 had to
// apologise for not having. The counts are what the page renders, so they are
// what this measures.
func TestAddingFilesToAnExistingModel(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "adder@example.com")

	resp, body := c.upload("Growing", map[string]string{"a.stl": "solid a"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	model := decodeModel(t, body)

	ct, part := filePart(t, "later.gcode", "G28 ; home")
	resp, body = c.addFile(model.ID, ct, part)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add file: got %d: %s", resp.StatusCode, body)
	}

	got := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", model.ID)))
	if got.FileCount != 2 || len(got.Files) != 2 {
		t.Fatalf("fileCount = %d with %d files, want 2 and 2", got.FileCount, len(got.Files))
	}
	if want := int64(len("solid a") + len("G28 ; home")); got.TotalSize != want {
		t.Errorf("totalSize = %d, want %d", got.TotalSize, want)
	}
	if final, temp := blobs(t, dir); len(final) != 2 || len(temp) != 0 {
		t.Errorf("blobs = %v, temp = %v, want 2 and none", final, temp)
	}
}

// Deleting a file takes the row *and* the bytes, and leaves the model. Checking
// only the row would pass while the disk filled up with files nothing points
// at; checking only the model would pass if the delete never happened.
func TestDeletingAFileLeavesTheModel(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "filekiller@example.com")

	resp, body := c.upload("Trimmed", map[string]string{
		"keep.stl": "solid keep", "drop.stl": "solid drop",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	model := decodeModel(t, body)
	var drop library.File
	for _, f := range model.Files {
		if f.Filename == "drop.stl" {
			drop = f
		}
	}
	if drop.ID == 0 {
		t.Fatalf("no drop.stl in %+v", model.Files)
	}

	resp, body = c.send(http.MethodDelete,
		fmt.Sprintf("/api/models/%d/files/%d", model.ID, drop.ID), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete file: got %d, want 204: %s", resp.StatusCode, body)
	}

	got := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", model.ID)))
	if got.FileCount != 1 || len(got.Files) != 1 || got.Files[0].Filename != "keep.stl" {
		t.Fatalf("after delete the model has %+v, want just keep.stl", got.Files)
	}
	if got.TotalSize != int64(len("solid keep")) {
		t.Errorf("totalSize = %d, want %d", got.TotalSize, len("solid keep"))
	}
	if final, temp := blobs(t, dir); len(final) != 1 || len(temp) != 0 {
		t.Errorf("blobs = %v, temp = %v, want 1 and none", final, temp)
	}

	// Deleting the last one leaves the model behind, empty. That is the point
	// of keeping models and files separate, and it is the case the detail page
	// has an empty state for - so `files` has to be present and empty, not
	// missing.
	resp, body = c.send(http.MethodDelete,
		fmt.Sprintf("/api/models/%d/files/%d", model.ID, got.Files[0].ID), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete last file: got %d: %s", resp.StatusCode, body)
	}
	raw := mustGet(t, c, fmt.Sprintf("/api/models/%d", model.ID))
	if !strings.Contains(raw, `"files":[]`) {
		t.Errorf("an empty model serves %s, want files as an empty array", raw)
	}
	if final, _ := blobs(t, dir); len(final) != 0 {
		t.Errorf("blobs = %v, want none", final)
	}
}

// Deleting a model takes the model, its file rows and every blob. The blobs are
// the half that a cascade alone would miss: Postgres deletes the rows and
// nothing on disk.
func TestDeletingAModelRemovesEverything(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "modelkiller@example.com")

	resp, body := c.upload("Doomed", map[string]string{
		"a.stl": "solid a", "b.stl": "solid b", "c.gcode": "G28",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	model := decodeModel(t, body)
	// A second model, to catch a delete that is too enthusiastic about which
	// rows or which blobs it takes.
	resp, body = c.upload("Bystander", map[string]string{"z.stl": "solid z"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("second upload: got %d: %s", resp.StatusCode, body)
	}
	bystander := decodeModel(t, body)

	resp, body = c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d", model.ID), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204: %s", resp.StatusCode, body)
	}

	if resp, out := c.get(fmt.Sprintf("/api/models/%d", model.ID)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the deleted model still reads: got %d: %s", resp.StatusCode, out)
	}
	// The files are gone as rows, not just unreachable through their model.
	for _, f := range model.Files {
		path := fmt.Sprintf("/api/models/%d/files/%d", model.ID, f.ID)
		if resp, _ := c.get(path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s still downloads: got %d", path, resp.StatusCode)
		}
	}
	if final, temp := blobs(t, dir); len(final) != 1 || len(temp) != 0 {
		t.Errorf("blobs = %v, temp = %v, want only the bystander's", final, temp)
	}
	if got := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", bystander.ID))); got.FileCount != 1 {
		t.Errorf("the bystander lost files: %+v", got)
	}

	// Deleting it twice is a 404, not a 500: the second request is what a
	// double-click sends.
	if resp, _ := c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d", model.ID), ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("deleting twice: got %d, want 404", resp.StatusCode)
	}
}

// The model goes even when its bytes cannot be unlinked.
//
// The ordering is deliberate: the database transaction commits first, and only
// then are the blobs removed. If it were the other way round a failed unlink
// would abort the delete and leave a model the user cannot get rid of, which is
// exactly the dead end milestone 1 shipped.
//
// A directory with something in it stands in for an unlinkable blob because
// os.Remove refuses it with ENOTEMPTY for every uid, where chmod games depend
// on not running as root.
//
// What this does *not* prove is the ordering itself - code that unlinked first
// and swallowed the error would also pass. That part is held by reading the
// function, and a test for it would be timing luck rather than a measurement.
func TestDeletedModelSurvivesAnUnlinkableBlob(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "stuck@example.com")

	resp, body := c.upload("Wedged", map[string]string{"a.stl": "solid a"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	model := decodeModel(t, body)

	final, _ := blobs(t, dir)
	if len(final) != 1 {
		t.Fatalf("blobs = %v, want exactly one to wedge", final)
	}
	blob := filepath.Join(dir, final[0])
	if err := os.Remove(blob); err != nil {
		t.Fatalf("remove blob: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(blob, "occupied"), 0o755); err != nil {
		t.Fatalf("wedge blob: %v", err)
	}

	resp, body = c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d", model.ID), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204: %s", resp.StatusCode, body)
	}
	if resp, _ := c.get(fmt.Sprintf("/api/models/%d", model.ID)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the model outlived a failed unlink: got %d", resp.StatusCode)
	}
	if strings.TrimSpace(mustGet(t, c, "/api/models")) != "[]" {
		t.Error("the library still lists a model whose blob could not be removed")
	}
}

// A download is the stored bytes, offered as a download, under the name the
// user uploaded.
func TestDownloadServesTheStoredFile(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "downloader@example.com")

	const contents = "solid benchy\nfacet normal 0 0 1\nendsolid\n"
	resp, body := c.upload("Benchy", map[string]string{"benchy.stl": contents})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	model := decodeModel(t, body)

	resp, out := c.get(fmt.Sprintf("/api/models/%d/files/%d", model.ID, model.Files[0].ID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: got %d: %s", resp.StatusCode, out)
	}
	if out != contents {
		t.Errorf("downloaded %q, want %q", out, contents)
	}
	// Attachment, and the original filename: a browser that renders an
	// uploaded file inline is a stored-XSS delivery mechanism, and one that
	// saves it as "42" is useless.
	if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") ||
		!strings.Contains(got, "benchy.stl") {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := resp.Header.Get("Content-Type"); got != model.Files[0].ContentType {
		t.Errorf("Content-Type = %q, want the stored %q", got, model.Files[0].ContentType)
	}
	if got := resp.Header.Get("Content-Length"); got != fmt.Sprint(len(contents)) {
		t.Errorf("Content-Length = %q, want %d", got, len(contents))
	}
}

// The stored content type is sniffed from the bytes, never taken from the
// client. A forged header is the whole point: a later milestone decides whether
// a file is a thumbnail from this field, and "the uploader said so" is not
// something to build that on.
func TestContentTypeIsSniffedNotTrusted(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "forger@example.com")

	var buf strings.Builder
	mw := multipart.NewWriter(&buf)
	head := make(textproto.MIMEHeader)
	head.Set("Content-Disposition", `form-data; name="file"; filename="totally.png"`)
	head.Set("Content-Type", "image/png")
	w, err := mw.CreatePart(head)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	// Arbitrary binary, chosen so http.DetectContentType has nothing to
	// recognise and answers application/octet-stream. HTML or a real PNG would
	// be a weaker test: both sniff to something specific, so the assertion
	// would still pass if the header were consulted as a fallback for bytes
	// nothing recognises - which is exactly the hole being closed.
	if _, err := w.Write([]byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0x00, 0x7f}); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	resp, body := c.post("Forged", mw.FormDataContentType(), strings.NewReader(buf.String()))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	got := decodeModel(t, body)
	if len(got.Files) != 1 {
		t.Fatalf("got %d files", len(got.Files))
	}
	if ct := got.Files[0].ContentType; ct != "application/octet-stream" {
		t.Errorf("contentType = %q, want application/octet-stream - the client's claim was believed", ct)
	}
	// The domain type still comes from the extension, which is a different
	// question: "what kind of thing is this in the library" is the user's to
	// declare by naming the file, where "what bytes are these" is not.
	if got.Files[0].Type != "image" {
		t.Errorf("type = %q, want image - the extension decides the domain type", got.Files[0].Type)
	}

	// The stored value is only half of it: what the browser acts on is the
	// header on the download, and image/png there is what would make a forged
	// upload render as a picture.
	down, _ := c.get(fmt.Sprintf("/api/models/%d/files/%d", got.ID, got.Files[0].ID))
	if ct := down.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("served Content-Type = %q, want application/octet-stream", ct)
	}
}

// Everything milestone 2 added, tried against somebody else's model. All of it
// must be 404 rather than 403: a 403 would confirm the model exists, which is
// itself something the intruder should not learn.
func TestAnotherUserCannotTouchAModel(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})

	owner := signIn(t, ts, "owner3@example.com")
	resp, body := owner.upload("Private", map[string]string{"secret.stl": "solid secret"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("owner upload: got %d: %s", resp.StatusCode, body)
	}
	model := decodeModel(t, body)
	file := fmt.Sprintf("/api/models/%d/files/%d", model.ID, model.Files[0].ID)
	self := fmt.Sprintf("/api/models/%d", model.ID)

	intruder := signIn(t, ts, "intruder3@example.com")
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, self, ""},
		{http.MethodPut, self, edits("Stolen", "", "", "")},
		{http.MethodDelete, self, ""},
		{http.MethodGet, file, ""},
		{http.MethodDelete, file, ""},
	} {
		resp, out := intruder.send(tc.method, tc.path, tc.body)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: got %d, want 404: %s", tc.method, tc.path, resp.StatusCode, out)
		}
	}

	// And none of it touched anything.
	got := decodeModel(t, mustGet(t, owner, self))
	if got.Name != "Private" || got.FileCount != 1 {
		t.Errorf("the owner's model changed: %+v", got)
	}
	if final, _ := blobs(t, dir); len(final) != 1 {
		t.Errorf("blobs = %v, want the owner's one", final)
	}
}

// A file id that belongs to a different model is a 404 too. Without the join on
// model_id the handler would happily delete or serve any file whose owner
// happens to match, which is the sort of thing that only shows up once two
// models exist.
func TestFileIDFromAnotherModelIsNotFound(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "mixer@example.com")

	resp, body := c.upload("First", map[string]string{"a.stl": "solid a"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	first := decodeModel(t, body)
	resp, body = c.upload("Second", map[string]string{"b.stl": "solid b"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	second := decodeModel(t, body)

	crossed := fmt.Sprintf("/api/models/%d/files/%d", first.ID, second.Files[0].ID)
	if resp, out := c.get(crossed); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s: got %d, want 404: %s", crossed, resp.StatusCode, out)
	}
	if resp, out := c.send(http.MethodDelete, crossed, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE %s: got %d, want 404: %s", crossed, resp.StatusCode, out)
	}
	if got := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", second.ID))); got.FileCount != 1 {
		t.Errorf("the crossed delete took the other model's file: %+v", got)
	}
}

// An unexpected failure is a sentence, not the wrapped error. huma renders
// err.Error() as the problem detail, and these errors carry a filesystem path
// and a storage key all the way up from the disk layer. The trigger is real:
// a blob removed out from under its row is exactly what a half-restored backup
// looks like.
func TestUnexpectedFailuresDoNotLeakInternals(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "leaks@example.com")

	resp, body := c.upload("Benchy", map[string]string{"benchy.stl": "solid benchy\n"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	model := decodeModel(t, body)

	final, _ := blobs(t, dir)
	if len(final) != 1 {
		t.Fatalf("blobs = %v, want one", final)
	}
	if err := os.Remove(filepath.Join(dir, final[0])); err != nil {
		t.Fatalf("remove blob: %v", err)
	}

	resp, out := c.get(fmt.Sprintf("/api/models/%d/files/%d", model.ID, model.Files[0].ID))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("download: got %d: %s", resp.StatusCode, out)
	}
	// Each of these is a different leak: the wrapping says which internal call
	// failed, the storage key names a file the caller was never told about, and
	// the directory is the server's filesystem layout.
	for _, secret := range []string{"library:", final[0], dir} {
		if strings.Contains(out, secret) {
			t.Errorf("problem detail leaks %q: %s", secret, out)
		}
	}

	// The other half of the same rule: the app's own sentences must still get
	// through. Update shares its error mapping with upload, and masking that
	// mapping's 422 would leave the edit dialog with nothing to show. A blank
	// name reaches it - huma's `required` means present, not non-empty, so this
	// is the service's own refusal and not schema validation.
	resp, out = c.send(http.MethodPut, fmt.Sprintf("/api/models/%d", model.ID),
		`{"name":"   ","description":"","printTips":"","sourceUrl":""}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("update: got %d: %s", resp.StatusCode, out)
	}
	if !strings.Contains(out, "a model needs a name") {
		t.Errorf("422 should say what was wrong: %s", out)
	}
}

// fixture reads one of the parser's real slicer captures. The integration tests
// use the same bytes as the unit tests on purpose: a hand-written header would
// let the wiring pass while the thing it is wired to could not read a file any
// slicer actually writes.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "gcode", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// Extraction happens once, during the upload, and everything after it is a read
// of what was stored. So this checks both ends: the create response, which is
// built from the value in memory, and the detail re-read, which is built from
// the jsonb column. They are two different code paths and only one of them
// survives a restart.
func TestUploadExtractsSliceSettings(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "slicer@example.com")

	ct, part := filePart(t, "plate-1.gcode", fixture(t, "prusaslicer.gcode"))
	resp, body := c.post("Benchy", ct, part)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	created := decodeModel(t, body)

	check := func(where string, m library.ModelDetail) {
		t.Helper()
		if len(m.Files) != 1 {
			t.Fatalf("%s: got %d files, want 1", where, len(m.Files))
		}
		meta := m.Files[0].ExtractedMeta
		if meta == nil {
			t.Fatalf("%s: no extractedMeta on a PrusaSlicer file", where)
		}
		if meta.Slicer != "PrusaSlicer" || meta.SlicerVersion != "2.9.2" {
			t.Errorf("%s: slicer = %q %q, want PrusaSlicer 2.9.2", where, meta.Slicer, meta.SlicerVersion)
		}
		// One value from the header block and one from the footer statistics,
		// because they arrive through the two separate reads the parser makes
		// and a window that came back empty would still leave the other set.
		if meta.LayerHeightMm == nil || *meta.LayerHeightMm != 0.2 {
			t.Errorf("%s: layerHeightMm = %s, want 0.2", where, show(meta.LayerHeightMm))
		}
		if meta.PrintTimeSeconds == nil || *meta.PrintTimeSeconds != 57 {
			t.Errorf("%s: printTimeSeconds = %s, want 57", where, show(meta.PrintTimeSeconds))
		}
	}
	check("create", created)
	check("detail", decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", created.ID))))
}

// The two upload routes are separate handlers, and only one of them is exercised
// by the test above. Without this, extraction could be wired into Create alone
// and every file added to an existing model would silently lose its settings.
func TestAddFileExtractsSliceSettings(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "addfile@example.com")

	ct, part := filePart(t, "body.stl", "solid body")
	resp, body := c.post("Benchy", ct, part)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d: %s", resp.StatusCode, body)
	}
	model := decodeModel(t, body)

	ct, part = filePart(t, "plate-1.gcode", fixture(t, "orcaslicer_2.3.gcode"))
	resp, body = c.addFile(model.ID, ct, part)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add file: got %d: %s", resp.StatusCode, body)
	}

	var added library.File
	if err := json.Unmarshal([]byte(body), &added); err != nil {
		t.Fatalf("decode added file: %v (body %q)", err, body)
	}
	meta := added.ExtractedMeta
	if meta == nil {
		t.Fatal("no extractedMeta on the added G-code file")
	}
	if meta.Slicer != "OrcaSlicer" || meta.SlicerVersion != "2.3.2-dev" {
		t.Errorf("slicer = %q %q, want OrcaSlicer 2.3.2-dev", meta.Slicer, meta.SlicerVersion)
	}

	// And the STL uploaded alongside it must still have none, so this cannot
	// pass by attaching the same settings to every file in the model.
	detail := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", model.ID)))
	for _, f := range detail.Files {
		got := f.ExtractedMeta != nil
		if want := f.Filename == "plate-1.gcode"; got != want {
			t.Errorf("%s: extractedMeta present = %v, want %v", f.Filename, got, want)
		}
	}
}

// show prints what a pointer field holds, because %v on a *float64 prints its
// address and a failure message with an address in it says nothing.
func show[T any](p *T) string {
	if p == nil {
		return "absent"
	}
	return fmt.Sprint(*p)
}

// A file we could not read must come back with the key absent, not present and
// empty. The panel decides whether to render from whether the key is there, so
// a `"extractedMeta": {}` would draw a heading with nothing under it - which
// reads as a panel that failed rather than a file that never said.
func TestFilesWithoutSliceSettingsOmitTheKey(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "nometa@example.com")

	// The same fixture with its generator line gone: still valid G-code, still
	// sniffed as G-code, but from a slicer we cannot name.
	anonymous := strings.ReplaceAll(fixture(t, "prusaslicer.gcode"), "generated by PrusaSlicer", "written by SomeoneElse")

	resp, body := c.upload("Benchy", map[string]string{
		"body.stl":        "solid body",
		"anonymous.gcode": anonymous,
		// A pasted header saved beside the model. It parses perfectly well as
		// G-code, and that is the point: what decides is the file's type, not
		// whether its bytes happen to be readable. Without this the type check
		// could be deleted and every test here would still pass.
		"slicer-log.txt": fixture(t, "prusaslicer.gcode"),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	model := decodeModel(t, body)

	// Read the raw JSON, not the decoded struct: a *gcode.Meta field decodes a
	// missing key and a null to the same nil, so the struct cannot tell the two
	// apart and this would pass either way.
	var raw struct {
		Files []map[string]json.RawMessage `json:"files"`
	}
	detail := mustGet(t, c, fmt.Sprintf("/api/models/%d", model.ID))
	if err := json.Unmarshal([]byte(detail), &raw); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(raw.Files) != 3 {
		t.Fatalf("got %d files, want 3", len(raw.Files))
	}
	for _, f := range raw.Files {
		if _, ok := f["extractedMeta"]; ok {
			t.Errorf("file %s carries extractedMeta: %s", f["filename"], f["extractedMeta"])
		}
	}
}
