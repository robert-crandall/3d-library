package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/robert-crandall/go-home-server/db"

	"github.com/robert-crandall/3d-library/internal/library"
)

// logIn signs an already-registered user into a *different* server, which is
// what makes a restart testable: a fresh httptest.Server is a fresh cookie
// host, so the original client's jar is no use and signIn would try to register
// the same email twice.
func logIn(t *testing.T, ts *httptest.Server, email string) *client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	c := &client{t: t, ts: ts, hc: &http.Client{Jar: jar}}
	resp, body := c.send(http.MethodPost, "/api/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"correct-horse-battery"}`, email))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("log in %s: got %d: %s", email, resp.StatusCode, body)
	}
	return c
}

func duplicates(t *testing.T, c *client) library.Duplicates {
	t.Helper()
	resp, body := c.get("/api/duplicates")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get duplicates: got %d, want 200: %s", resp.StatusCode, body)
	}
	var out library.Duplicates
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode duplicates: %v (body %q)", err, body)
	}
	return out
}

// scan starts a scan and waits for it to finish, so a test asserts on a settled
// library rather than on a race. Waiting on the server's own status is what
// makes this reliable without a sleep.
func scan(t *testing.T, c *client) library.Duplicates {
	t.Helper()
	resp, body := c.send(http.MethodPost, "/api/duplicates/scan", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan: got %d, want 200: %s", resp.StatusCode, body)
	}
	var found library.Duplicates
	waitFor(t, "the scan to finish", func() bool {
		found = duplicates(t, c)
		return !found.Status.Running
	})
	return found
}

// scanStatus starts a scan and returns what the POST said, without waiting.
// The tests that care about the running scan itself need this; the ones that
// only want the answer use scan.
func scanStatus(t *testing.T, c *client) library.ScanStatus {
	t.Helper()
	resp, body := c.send(http.MethodPost, "/api/duplicates/scan", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan: got %d, want 200: %s", resp.StatusCode, body)
	}
	var out library.ScanStatus
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode scan status: %v (body %q)", err, body)
	}
	return out
}

// uploadOne makes a one-file model and returns the model and its file.
func uploadOne(t *testing.T, c *client, model, filename, contents string) (int64, int64) {
	t.Helper()
	resp, body := c.upload(model, map[string]string{filename: contents})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload %s: got %d: %s", model, resp.StatusCode, body)
	}
	m := decodeModel(t, body)
	if len(m.Files) != 1 {
		t.Fatalf("upload %s: got %d files, want 1", model, len(m.Files))
	}
	return m.ID, m.Files[0].ID
}

// hashes reads the content_hash column straight out of the database, because
// the API deliberately never exposes which files were hashed - and "hashed only
// the candidates" is a claim about work not done, which no response can show.
func hashes(t *testing.T, dbURL string) map[string]bool {
	t.Helper()
	ctx := context.Background()
	conn, err := db.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	rows, err := conn.Query(ctx,
		`SELECT filename, content_hash IS NOT NULL FROM model_files`)
	if err != nil {
		t.Fatalf("read hashes: %v", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var name string
		var hashed bool
		if err := rows.Scan(&name, &hashed); err != nil {
			t.Fatalf("read hashes: %v", err)
		}
		out[name] = hashed
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read hashes: %v", err)
	}
	return out
}

// The whole feature in one library: a real duplicate pair across two models, a
// pair that shares only a size, and a file whose size is unique.
//
// Three claims at once, and they are the three that can each be broken alone:
// the pair groups, the same-size-different-content pair does not (AC2), and the
// unique-size file is never even opened (AC3). A test with only the duplicate
// pair would pass against an implementation that hashes the entire library.
func TestDuplicateScanGroupsIdenticalFilesOnly(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	// Same bytes, different names, different models - a model downloaded twice.
	benchy, benchyFile := uploadOne(t, c, "Benchy", "benchy.stl", "solid benchy")
	copyModel, copyFile := uploadOne(t, c, "Benchy (2)", "3dbenchy.stl", "solid benchy")
	// Same length as each other, different bytes. Both get hashed and neither
	// groups, which is the difference between a size match and a duplicate.
	uploadOne(t, c, "Cube", "cube.stl", "solid AAAAA")
	uploadOne(t, c, "Cone", "cone.stl", "solid BBBBB")
	// Unique length. Never opened.
	uploadOne(t, c, "Whistle", "whistle.stl", "solid whistle - a length nothing else has")

	found := scan(t, c)

	if len(found.Groups) != 1 {
		t.Fatalf("got %d groups, want 1: %+v", len(found.Groups), found.Groups)
	}
	g := found.Groups[0]
	if g.Size != int64(len("solid benchy")) {
		t.Errorf("size = %d, want %d", g.Size, len("solid benchy"))
	}
	if g.Reclaimable != int64(len("solid benchy")) {
		t.Errorf("reclaimable = %d, want %d - one copy of two is freed",
			g.Reclaimable, len("solid benchy"))
	}
	if len(g.Files) != 2 {
		t.Fatalf("got %d files in the group, want 2: %+v", len(g.Files), g.Files)
	}
	byModel := map[int64]library.DuplicateFile{}
	for _, f := range g.Files {
		byModel[f.ModelID] = f
	}
	if f, ok := byModel[benchy]; !ok || f.FileID != benchyFile || f.Filename != "benchy.stl" || f.ModelName != "Benchy" {
		t.Errorf("Benchy's member is wrong: %+v", f)
	}
	if f, ok := byModel[copyModel]; !ok || f.FileID != copyFile || f.Filename != "3dbenchy.stl" || f.ModelName != "Benchy (2)" {
		t.Errorf("the copy's member is wrong: %+v", f)
	}

	if found.Status.Pending != 0 {
		t.Errorf("pending = %d, want 0 after a complete scan", found.Status.Pending)
	}
	if found.Status.ScannedAt == nil {
		t.Error("scannedAt is nil after a scan")
	}

	// AC3, read from the column rather than inferred: the four files that share
	// a size were hashed, and the one that does not was not.
	got := hashes(t, dbURL)
	want := map[string]bool{
		"benchy.stl": true, "3dbenchy.stl": true,
		"cube.stl": true, "cone.stl": true,
		"whistle.stl": false,
	}
	for name, wantHashed := range want {
		if got[name] != wantHashed {
			t.Errorf("%s hashed = %v, want %v", name, got[name], wantHashed)
		}
	}
	if found.Status.Total != 4 {
		t.Errorf("total = %d, want 4 candidates", found.Status.Total)
	}
}

// A library where every size is unique reports nothing found, and says when it
// looked. AC4's "clear result rather than an empty list" is the timestamp: an
// empty group list with no scannedAt is indistinguishable from never having
// scanned.
func TestDuplicateScanWithNothingToFindStillReportsWhenItLooked(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	uploadOne(t, c, "Benchy", "benchy.stl", "a")
	uploadOne(t, c, "Cube", "cube.stl", "bb")

	before := duplicates(t, c)
	if before.Status.ScannedAt != nil {
		t.Error("scannedAt is set before any scan has run")
	}

	found := scan(t, c)
	if len(found.Groups) != 0 {
		t.Errorf("got %d groups, want none: %+v", len(found.Groups), found.Groups)
	}
	if found.Status.ScannedAt == nil {
		t.Fatal("scannedAt is nil - the page cannot tell 'nothing found' from 'never scanned'")
	}
	if found.Status.Total != 0 || found.Status.Hashed != 0 {
		t.Errorf("total/hashed = %d/%d, want 0/0 - nothing shares a size, so nothing should be read",
			found.Status.Total, found.Status.Hashed)
	}
	if found.Status.Pending != 0 {
		t.Errorf("pending = %d, want 0", found.Status.Pending)
	}
}

// Deleting one copy frees its blob and leaves the other's alone.
//
// The second half is the whole point. Two rows with identical content could
// plausibly share a blob, and if they did, deleting one would silently destroy
// the other. They do not - storage keys are 16 random bytes per upload - and
// this asserts it by downloading the survivor after the delete rather than by
// trusting the comment that says so.
//
// It also covers AC5's collapse and AC7's empty model in the same run, because
// those are the same delete.
func TestDeletingADuplicateLeavesTheOtherCopyIntact(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "owner@example.com")

	keeper, keeperFile := uploadOne(t, c, "Benchy", "benchy.stl", "solid benchy")
	doomed, doomedFile := uploadOne(t, c, "Benchy (2)", "3dbenchy.stl", "solid benchy")

	if found := scan(t, c); len(found.Groups) != 1 {
		t.Fatalf("got %d groups before the delete, want 1", len(found.Groups))
	}
	final, sidecars, _ := blobs(t, dir)
	if len(final) != 2 {
		t.Fatalf("got %d blobs before the delete, want 2 - identical files must not share one", len(final))
	}

	resp, body := c.send(http.MethodDelete,
		fmt.Sprintf("/api/models/%d/files/%d", doomed, doomedFile), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete the duplicate: got %d, want 204: %s", resp.StatusCode, body)
	}

	// The survivor still downloads, byte for byte.
	resp, body = c.get(fmt.Sprintf("/api/models/%d/files/%d", keeper, keeperFile))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download the survivor: got %d, want 200", resp.StatusCode)
	}
	if body != "solid benchy" {
		t.Errorf("survivor's bytes = %q, want %q - deleting one copy took the other's blob",
			body, "solid benchy")
	}

	// Exactly one blob gone, and no orphaned sidecar.
	afterFinal, afterSidecars, temp := blobs(t, dir)
	if len(afterFinal) != 1 {
		t.Errorf("got %d blobs after the delete, want 1", len(afterFinal))
	}
	if len(afterSidecars) != len(sidecars) && len(afterSidecars) != 0 {
		t.Errorf("got %d sidecars after the delete, want at most the %d there were",
			len(afterSidecars), len(sidecars))
	}
	if len(temp) != 0 {
		t.Errorf("got %d temp files, want 0", len(temp))
	}

	// AC5: the group is gone without a rescan, because a group of one is not a
	// group and nothing had to be bookkept to make that true.
	after := duplicates(t, c)
	if len(after.Groups) != 0 {
		t.Errorf("got %d groups after the delete, want 0: %+v", len(after.Groups), after.Groups)
	}

	// AC7: the emptied model is still in the library with no files.
	m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", doomed)))
	if len(m.Files) != 0 {
		t.Errorf("got %d files on the emptied model, want 0", len(m.Files))
	}
	if m.Name != "Benchy (2)" {
		t.Errorf("the emptied model is %q, want it still named Benchy (2)", m.Name)
	}
}

// Another user's byte-identical file is invisible, and - the half that is easy
// to get wrong - it does not even make one of my files a candidate.
//
// The second fixture is what makes this discriminating. "smuggled.stl" is the
// only file of its size in MY library, so it must never be hashed; it shares a
// size only with a stranger's file. Dropping the user_id predicate from the
// *inner* candidate subquery leaves the outer scoping intact - the groups stay
// clean and a weaker test still passes - while quietly breaking AC3 by reading
// files whose size is unique to me.
func TestDuplicateScanIgnoresOtherUsersFiles(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})

	stranger := signIn(t, ts, "stranger@example.com")
	uploadOne(t, stranger, "Theirs", "theirs.stl", "solid benchy")
	uploadOne(t, stranger, "Theirs Too", "smuggle-bait.stl", "solid smuggled!")

	owner := signIn(t, ts, "owner@example.com")
	uploadOne(t, owner, "Mine", "mine.stl", "solid benchy")
	uploadOne(t, owner, "Also Mine", "smuggled.stl", "solid smuggled!")

	found := scan(t, owner)
	if len(found.Groups) != 0 {
		t.Fatalf("got %d groups, want none - my two files differ and the matches are a stranger's: %+v",
			len(found.Groups), found.Groups)
	}
	if found.Status.Total != 0 {
		t.Errorf("total = %d, want 0 candidates - every one of my sizes is unique to me",
			found.Status.Total)
	}

	got := hashes(t, dbURL)
	for _, name := range []string{"mine.stl", "smuggled.stl", "theirs.stl", "smuggle-bait.stl"} {
		if got[name] {
			t.Errorf("%s was hashed - my scan must not read across users, in either direction", name)
		}
	}
}

// A rescan with nothing new reads nothing, because a hash is permanently
// correct once computed.
//
// A weaker version would just check the groups are still there, which passes
// against an implementation that re-reads the whole library every time - the
// exact regression this feature exists to avoid.
func TestRescanDoesNotRehashWhatItAlreadyKnows(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	uploadOne(t, c, "Benchy", "benchy.stl", "solid benchy")
	uploadOne(t, c, "Benchy (2)", "3dbenchy.stl", "solid benchy")

	first := scan(t, c)
	if first.Status.Total != 2 {
		t.Fatalf("first scan read %d files, want 2", first.Status.Total)
	}

	second := scan(t, c)
	if second.Status.Total != 0 {
		t.Errorf("second scan read %d files, want 0 - already-hashed files must not be re-read",
			second.Status.Total)
	}
	if len(second.Groups) != 1 {
		t.Errorf("got %d groups after the rescan, want 1 - the stored hashes are the answer",
			len(second.Groups))
	}
	if second.Status.ScannedAt == nil {
		t.Error("scannedAt is nil after the second scan")
	}
	if first.Status.ScannedAt != nil && second.Status.ScannedAt != nil &&
		!second.Status.ScannedAt.After(*first.Status.ScannedAt) {
		t.Error("scannedAt did not move forward on a scan that found nothing new")
	}
}

// A file whose blob cannot be read leaves the library incompletely hashed, and
// that has to survive a restart.
//
// This is the failure the whole `pending` field exists for. An in-memory "last
// run had errors" flag is gone at the next start, and then a library with one
// unreadable half of a genuine pair renders a confident "No duplicate files".
// Counting the still-unhashed candidates from the rows cannot forget.
//
// The restart is the load-bearing half: a `pending` held in memory passes the
// single-process version of this test.
func TestAnUnreadableBlobLeavesTheLibraryPending(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "owner@example.com")

	uploadOne(t, c, "Benchy", "benchy.stl", "solid benchy")
	uploadOne(t, c, "Benchy (2)", "3dbenchy.stl", "solid benchy")
	uploadOne(t, c, "Cube", "cube.stl", "solid cubes!")
	uploadOne(t, c, "Cone", "cone.stl", "solid cones!")

	// Take one blob away behind the app's back, the way a bad backup restore or
	// a stray rm would.
	final, _, _ := blobs(t, dir)
	if len(final) != 4 {
		t.Fatalf("got %d blobs, want 4", len(final))
	}
	if err := os.Remove(filepath.Join(dir, final[0])); err != nil {
		t.Fatalf("remove a blob: %v", err)
	}

	found := scan(t, c)
	if found.Status.Pending != 1 {
		t.Errorf("pending = %d, want 1 - the unreadable file is still unhashed", found.Status.Pending)
	}
	if found.Status.Error == "" {
		t.Error("the run reported no error even though a file could not be read")
	}
	// The rest of the library still got hashed: one bad blob does not abandon
	// the run.
	hashed := 0
	for _, ok := range hashes(t, dbURL) {
		if ok {
			hashed++
		}
	}
	if hashed != 3 {
		t.Errorf("%d files hashed, want 3 - a read failure must not abort the other candidates", hashed)
	}

	// Restart: a brand new server over the same database and directory, with no
	// memory of the run at all.
	restarted := newTestServer(t, pool, library.Options{Dir: dir})
	fresh := logIn(t, restarted, "owner@example.com")
	after := duplicates(t, fresh)
	if after.Status.Pending != 1 {
		t.Errorf("pending = %d after a restart, want 1 - it must be counted from the rows, not held in memory",
			after.Status.Pending)
	}
	if after.Status.ScannedAt == nil {
		t.Error("scannedAt did not survive the restart")
	}
}

// holdFile parks a transaction on one model_files row, which is what the scan's
// per-file UPDATE needs. Parking the scan mid-run is the only way to make "a
// second POST while one is running" a fact rather than a hope: without it the
// two files hash in under a millisecond and the second POST almost always
// arrives after the first scan has already finished, which proves nothing.
func holdFile(t *testing.T, dbURL string, id int64) (release func(), pid int32) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("holder connect: %v", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("holder begin: %v", err)
	}
	var locked int64
	if err := tx.QueryRow(ctx,
		"SELECT id FROM model_files WHERE id = $1 FOR UPDATE", id).Scan(&locked); err != nil {
		t.Fatalf("hold file %d: %v", id, err)
	}
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatalf("read the holder's pid: %v", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = tx.Rollback(ctx)
			conn.Close()
		})
	}, pid
}

// Scanning while a scan is running does nothing, so a double-clicked button
// cannot start two - and the interleaving is forced rather than raced for.
func TestScanningTwiceDoesNotStartTwoScans(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	uploadOne(t, c, "Benchy", "benchy.stl", "solid benchy")
	_, second := uploadOne(t, c, "Benchy (2)", "3dbenchy.stl", "solid benchy")

	// The scan works through candidates by ascending id, so locking the second
	// one leaves it blocked on that UPDATE with exactly one file hashed.
	release, holderPID := holdFile(t, dbURL, second)
	defer release()

	first := scanStatus(t, c)
	if !first.Running {
		t.Fatalf("the first scan did not start: %+v", first)
	}
	waitForBlockedBackends(t, dbURL, holderPID, 1)

	var parked library.ScanStatus
	waitFor(t, "the scan to report its first file", func() bool {
		parked = duplicates(t, c).Status
		return parked.Running && parked.Total == 2 && parked.Hashed == 1
	})

	// The second POST, with the first scan demonstrably still running.
	again := scanStatus(t, c)
	if !again.Running || again.Total != 2 || again.Hashed != 1 {
		t.Errorf("second scan: %+v, want the first one's progress untouched "+
			"(a second run resets total and hashed to 0)", again)
	}

	release()

	var found library.Duplicates
	waitFor(t, "the scan to finish", func() bool {
		found = duplicates(t, c)
		return !found.Status.Running
	})
	if found.Status.Hashed != 2 {
		t.Errorf("hashed = %d, want 2", found.Status.Hashed)
	}
	if len(found.Groups) != 1 {
		t.Errorf("got %d groups, want 1", len(found.Groups))
	}
}

// Deleting a file while a model delete holds that model must not deadlock.
//
// This is the inversion M11 recorded and left: DeleteFile used to be one DELETE
// that took the *file* row first, and then thumbnail_file_id's ON DELETE SET
// NULL took the model - the opposite order to DeleteModel, BulkDelete and
// lockModels. The duplicates screen is a page of delete-file buttons, which is
// what made it worth fixing now.
//
// Forced rather than hoped for. A holder parks on the version row, so
// DeleteModel stalls on its second statement while already holding the parent.
// DeleteFile is then aimed at the file the parent has pinned as its thumbnail -
// pinned matters, because the referential action only locks model rows that
// actually reference the file, so an unpinned file never closes the cycle.
// Pre-fix it grabs the file row and queues on the model; releasing the holder
// lets DeleteModel reach its cascading file delete, which needs the row
// DeleteFile is holding, and Postgres kills one side with 40P01 - a 500.
//
// With the fix DeleteFile queues on the model before touching anything, so
// there is no cycle. Both outcomes below are then correct: DeleteModel wins the
// row it was already holding, and DeleteFile finds the model gone. A 500 is the
// only illegal answer.
func TestDeletingAFileDuringAModelDeleteDoesNotDeadlock(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	// A real PNG, because pinning a thumbnail needs a file that actually has
	// one - which is also what makes the referential action below fire.
	parent, parentFile := uploadOne(t, c, "Benchy", "benchy.png", thumbFixture(t, "render.png"))
	version, _ := uploadOne(t, c, "Benchy v2", "v2.stl", "solid v2")
	mustAttach(t, c, version, &parent)

	// Pinned on purpose: an unpinned file's delete never touches a model row,
	// so the cycle this test forces would not exist and it would pass against
	// the unfixed code.
	resp, body := c.send(http.MethodPut, fmt.Sprintf("/api/models/%d/thumbnail", parent),
		fmt.Sprintf(`{"fileId":%d}`, parentFile))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pin the thumbnail: got %d, want 200: %s", resp.StatusCode, body)
	}

	release, holderPID := hold(t, dbURL, version)
	defer release()

	modelCodes := make(chan int, 1)
	fileCodes := make(chan int, 1)

	// Queues first, so it is the one holding the parent when the file delete
	// wants it.
	go report(modelCodes, func() int {
		resp, _ := c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d", parent), "")
		return resp.StatusCode
	})
	waitForBlockedBackends(t, dbURL, holderPID, 1)

	go report(fileCodes, func() int {
		resp, _ := c.send(http.MethodDelete,
			fmt.Sprintf("/api/models/%d/files/%d", parent, parentFile), "")
		return resp.StatusCode
	})
	waitForBlockedBackends(t, dbURL, holderPID, 2)
	release()

	if code := <-modelCodes; code != http.StatusNoContent {
		t.Errorf("delete model: got %d, want 204 - it held the parent throughout", code)
	}
	if code := <-fileCodes; code != http.StatusNotFound {
		t.Errorf("delete file: got %d, want 404 - the model it belonged to was deleted first "+
			"(a 500 is a deadlock)", code)
	}
}
