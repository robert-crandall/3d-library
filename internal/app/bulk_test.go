package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/robert-crandall/go-home-server/db"

	"github.com/robert-crandall/3d-library/internal/library"
)

// Bulk actions are the first thing in this app that changes several models at
// once, and every test here is about that difference: that a set is resolved and
// judged as a set rather than a model at a time, that a refusal leaves the whole
// set untouched, and that the delete's confirmation is still true by the time
// the rows go.

// idList renders a selection the way the grid sends it.
func idList(v []int64) string {
	parts := make([]string, 0, len(v))
	for _, id := range v {
		parts = append(parts, fmt.Sprint(id))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func bulk(t *testing.T, c *client, action, body string) (*http.Response, string) {
	t.Helper()
	return c.send(http.MethodPost, "/api/models/bulk/"+action, body)
}

func mustBulk(t *testing.T, c *client, action, body string) {
	t.Helper()
	resp, out := bulk(t, c, action, body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("bulk %s: got %d, want 204: %s", action, resp.StatusCode, out)
	}
}

func preview(t *testing.T, c *client, modelIDs []int64) library.DeletePreview {
	t.Helper()
	resp, body := bulk(t, c, "delete-preview", `{"modelIds":`+idList(modelIDs)+`}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete-preview: got %d, want 200: %s", resp.StatusCode, body)
	}
	var out library.DeletePreview
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode preview: %v (body %q)", err, body)
	}
	return out
}

// modelTags reads the tag names on a model, sorted, so an additive assertion
// says what it means without depending on insertion order.
func modelTags(t *testing.T, c *client, id int64) []string {
	t.Helper()
	m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", id)))
	out := make([]string, 0, len(m.Tags))
	for _, tag := range m.Tags {
		out = append(out, tag.Name)
	}
	slices.Sort(out)
	return out
}

func categoryName(t *testing.T, c *client, id int64) string {
	t.Helper()
	m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", id)))
	if m.Category == nil {
		return ""
	}
	return m.Category.Name
}

// three uploads three one-file models and returns their ids in upload order.
func three(t *testing.T, c *client) []int64 {
	t.Helper()
	var out []int64
	for _, name := range []string{"Benchy", "Gridfinity", "Whistle"} {
		resp, body := c.upload(name, map[string]string{name + ".stl": "solid " + name})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("upload %s: got %d: %s", name, resp.StatusCode, body)
		}
		out = append(out, decodeModel(t, body).ID)
	}
	return out
}

// Tagging in bulk is additive and idempotent, which is two claims: a tag a model
// already has is not an error, and a tag it does not have is not lost when
// another model in the set already had it.
//
// A weaker version of this test would tag three clean models and count three
// rows. That passes against an implementation that inserts without ON CONFLICT
// and against one that skips a model whose first tag collided, which are the two
// ways this can actually go wrong.
func TestBulkTagsAreAdditiveAndIdempotent(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	models := three(t, c)
	printed := createTag(t, c, "printed")
	wip := createTag(t, c, "wip")

	// The first model already carries one of the two.
	mustBulk(t, c, "tags", fmt.Sprintf(`{"modelIds":[%d],"tagIds":[%d]}`, models[0], printed))

	mustBulk(t, c, "tags", fmt.Sprintf(`{"modelIds":%s,"tagIds":[%d,%d]}`,
		idList(models), printed, wip))

	for _, id := range models {
		if got := modelTags(t, c, id); !equalStrings(got, []string{"printed", "wip"}) {
			t.Errorf("model %d tags = %v, want [printed wip]", id, got)
		}
	}

	// And again, which must change nothing rather than duplicate anything.
	mustBulk(t, c, "tags", fmt.Sprintf(`{"modelIds":%s,"tagIds":[%d,%d]}`,
		idList(models), printed, wip))
	if got := modelTags(t, c, models[0]); !equalStrings(got, []string{"printed", "wip"}) {
		t.Errorf("after a repeat: model tags = %v, want [printed wip]", got)
	}
}

// Recategorizing replaces rather than adds, and it works on a model that had no
// category as well as on one that did.
func TestBulkRecategorizeReplaces(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	models := three(t, c)
	tools := decodeCategory(t, mustCreate(t, c, "/api/categories",
		`{"name":"Tools","color":"#2563eb"}`)).ID
	toys := decodeCategory(t, mustCreate(t, c, "/api/categories",
		`{"name":"Toys","color":"#e11d48"}`)).ID

	// One of them starts somewhere else, the other two start nowhere.
	mustBulk(t, c, "category", fmt.Sprintf(`{"modelIds":[%d],"categoryId":%d}`, models[0], toys))

	mustBulk(t, c, "category", fmt.Sprintf(`{"modelIds":%s,"categoryId":%d}`, idList(models), tools))

	for _, id := range models {
		if got := categoryName(t, c, id); got != "Tools" {
			t.Errorf("model %d category = %q, want Tools", id, got)
		}
	}
}

// Adding to a collection skips the models already in it, and the count the
// sidebar shows is right afterwards - which is the assertion that would catch a
// duplicate membership row that the model page would never reveal.
func TestBulkAddToCollectionSkipsMembers(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	models := three(t, c)
	coll := createCollection(t, c, "To print", "")
	mustAddToCollection(t, c, models[0], coll)

	mustBulk(t, c, "collection", fmt.Sprintf(`{"modelIds":%s,"collectionId":%d}`,
		idList(models), coll))

	if got := collectionCount(t, c, coll); got != 3 {
		t.Errorf("collection count = %d, want 3", got)
	}
	mustBulk(t, c, "collection", fmt.Sprintf(`{"modelIds":%s,"collectionId":%d}`,
		idList(models), coll))
	if got := collectionCount(t, c, coll); got != 3 {
		t.Errorf("after a repeat: collection count = %d, want 3", got)
	}
}

// The whole point of "all or nothing": one bad id refuses the request and the
// models that would have changed do not.
//
// All four mutations, because they are four statements with four different
// shapes - two counting CTEs, an UPDATE judged by rows-affected, and a locking
// delete - and getting one of them right says nothing about the others.
func TestBulkRefusesAForeignModelAndChangesNothing(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	owner := signIn(t, ts, "owner@example.com")
	other := signIn(t, ts, "other@example.com")

	mine := three(t, owner)
	theirs := three(t, other)
	tag := createTag(t, owner, "printed")
	cat := decodeCategory(t, mustCreate(t, owner, "/api/categories",
		`{"name":"Tools","color":"#2563eb"}`)).ID
	coll := createCollection(t, owner, "To print", "")

	mixed := idList(append(slices.Clone(mine), theirs[0]))
	for _, tc := range []struct{ action, body string }{
		{"tags", fmt.Sprintf(`{"modelIds":%s,"tagIds":[%d]}`, mixed, tag)},
		{"category", fmt.Sprintf(`{"modelIds":%s,"categoryId":%d}`, mixed, cat)},
		{"collection", fmt.Sprintf(`{"modelIds":%s,"collectionId":%d}`, mixed, coll)},
		{"delete-preview", fmt.Sprintf(`{"modelIds":%s}`, mixed)},
		{"delete", fmt.Sprintf(`{"modelIds":%s,"expectVersions":0,"expectFiles":4}`, mixed)},
	} {
		resp, body := bulk(t, owner, tc.action, tc.body)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s with a foreign id: got %d, want 404: %s", tc.action, resp.StatusCode, body)
		}
	}

	// Nothing of mine changed...
	for _, id := range mine {
		if got := modelTags(t, owner, id); len(got) != 0 {
			t.Errorf("model %d picked up tags %v from a refused request", id, got)
		}
		if got := categoryName(t, owner, id); got != "" {
			t.Errorf("model %d picked up category %q from a refused request", id, got)
		}
	}
	if got := collectionCount(t, owner, coll); got != 0 {
		t.Errorf("collection count = %d, want 0", got)
	}
	// ...and nothing of theirs did either, which is the half a 404 alone does
	// not prove: a refusal that had already written to their row would look
	// identical from here.
	for _, id := range theirs {
		if got := modelTags(t, other, id); len(got) != 0 {
			t.Errorf("the other user's model %d was tagged", id)
		}
		if got := categoryName(t, other, id); got != "" {
			t.Errorf("the other user's model %d was recategorized to %q", id, got)
		}
	}
	if _, body := other.get(fmt.Sprintf("/api/models/%d", theirs[0])); body == "" {
		t.Error("the other user's model is gone")
	}
	final, _, _ := blobs(t, dir)
	if len(final) != 6 {
		t.Errorf("got %d blobs, want 6 - a refused bulk delete unlinked something", len(final))
	}
}

// A tag id that belongs to somebody else is a 422 rather than a 404, because the
// models were addressable and it is the tag that was not. It also has to roll
// back: the CTE's insert runs to completion whether or not the outer query reads
// its count, so by the time the count is judged the good pairs are already in.
//
// This is the natural rollback test - no fault injection - and mutating the
// check to a warning leaves the tag on the models it did match.
func TestBulkTagsRollBackWhenOneTagIsForeign(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	owner := signIn(t, ts, "owner@example.com")
	other := signIn(t, ts, "other@example.com")

	models := three(t, owner)
	mine := createTag(t, owner, "printed")
	theirs := createTag(t, other, "secret")

	resp, body := bulk(t, owner, "tags",
		fmt.Sprintf(`{"modelIds":%s,"tagIds":[%d,%d]}`, idList(models), mine, theirs))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "unknown tag") {
		t.Errorf("message = %s, want it to name the tag", body)
	}

	for _, id := range models {
		if got := modelTags(t, owner, id); len(got) != 0 {
			t.Errorf("model %d kept %v from a rolled-back request", id, got)
		}
	}
}

func TestBulkRefusesAForeignCategoryAndCollection(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	owner := signIn(t, ts, "owner@example.com")
	other := signIn(t, ts, "other@example.com")

	models := three(t, owner)
	cat := decodeCategory(t, mustCreate(t, other, "/api/categories",
		`{"name":"Theirs","color":"#2563eb"}`)).ID
	coll := createCollection(t, other, "Theirs", "")

	for _, tc := range []struct{ action, body, want string }{
		{"category", fmt.Sprintf(`{"modelIds":%s,"categoryId":%d}`, idList(models), cat),
			"unknown category"},
		{"collection", fmt.Sprintf(`{"modelIds":%s,"collectionId":%d}`, idList(models), coll),
			"unknown collection"},
	} {
		resp, body := bulk(t, owner, tc.action, tc.body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422: %s", tc.action, resp.StatusCode, body)
		}
		if !strings.Contains(body, tc.want) {
			t.Errorf("%s message = %s, want %q", tc.action, body, tc.want)
		}
	}

	// The category one is the dangerous shape: SET category_id = (SELECT ...)
	// would write NULL here and silently uncategorize everything instead of
	// refusing, which is why setCategory does not do it either.
	for _, id := range models {
		if got := categoryName(t, owner, id); got != "" {
			t.Errorf("model %d category = %q, want none", id, got)
		}
	}
}

// The preview's three numbers are what the confirmation says out loud, so they
// have to be the numbers the delete then destroys - including the versions the
// grid never listed and their files, which is exactly what a client-side count
// would get wrong.
func TestBulkDeletePreviewMatchesWhatIsDestroyed(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "owner@example.com")

	// A root with two files and a version with one, plus a lone model. One of
	// those files is a PNG rather than an STL, because a PNG is what makes the
	// upload write a .thumb sidecar beside the blob: with STLs only, no sidecar
	// ever exists and the sidecar assertion at the end passes against a cleanup
	// path that forgets them entirely.
	resp, body := c.upload("Benchy", map[string]string{
		"a.stl": "solid a", "b.png": thumbFixture(t, "render.png")})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	root := decodeModel(t, body).ID
	resp, body = c.upload("Benchy v2", map[string]string{"c.stl": "solid c"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	version := decodeModel(t, body).ID
	mustAttach(t, c, version, &root)
	resp, body = c.upload("Whistle", map[string]string{"d.stl": "solid d"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", resp.StatusCode, body)
	}
	lone := decodeModel(t, body).ID

	// The grid only lists roots, so this is what a select-all would send.
	selection := []int64{root, lone}
	got := preview(t, c, selection)
	want := library.DeletePreview{Models: 2, Versions: 1, Files: 4}
	if got != want {
		t.Fatalf("preview = %+v, want %+v", got, want)
	}

	before, sidecarsBefore, _ := blobs(t, dir)
	if len(before) != 4 || len(sidecarsBefore) != 1 {
		t.Fatalf("got %d blobs and %d sidecars before the delete, want 4 and 1",
			len(before), len(sidecarsBefore))
	}

	mustBulk(t, c, "delete", fmt.Sprintf(`{"modelIds":%s,"expectVersions":%d,"expectFiles":%d}`,
		idList(selection), got.Versions, got.Files))

	// Every row, including the version nobody named.
	for _, id := range []int64{root, version, lone} {
		if resp, _ := c.get(fmt.Sprintf("/api/models/%d", id)); resp.StatusCode != http.StatusNotFound {
			t.Errorf("model %d survived: got %d, want 404", id, resp.StatusCode)
		}
	}
	// And every blob, and every thumbnail sidecar beside it. Orphaned bytes are
	// invisible to every other assertion in this file.
	final, sidecars, temp := blobs(t, dir)
	if len(final) != 0 || len(sidecars) != 0 || len(temp) != 0 {
		t.Errorf("left %d blobs, %d sidecars and %d temp files behind, want none",
			len(final), len(sidecars), len(temp))
	}
}

// The confirmation is a promise about a set, and the set can move while the
// dialog is open. Deleting anyway would destroy a version the user was never
// told about, which is unrecoverable, so the numbers go back with the request
// and are rechecked under the locks.
func TestBulkDeleteRefusesWhenTheSelectionGrew(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "owner@example.com")

	models := three(t, c)
	shown := preview(t, c, models[:1])

	// The other tab attaches a version to the model this dialog is about.
	mustAttach(t, c, models[1], &models[0])

	resp, body := bulk(t, c, "delete",
		fmt.Sprintf(`{"modelIds":%s,"expectVersions":%d,"expectFiles":%d}`,
			idList(models[:1]), shown.Versions, shown.Files))
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("got %d, want 409: %s", resp.StatusCode, body)
	}

	// Nothing destroyed, so the second confirmation has something to be about.
	for _, id := range models {
		if resp, _ := c.get(fmt.Sprintf("/api/models/%d", id)); resp.StatusCode != http.StatusOK {
			t.Errorf("model %d is gone after a refused delete: got %d", id, resp.StatusCode)
		}
	}
	final, _, _ := blobs(t, dir)
	if len(final) != 3 {
		t.Errorf("got %d blobs, want 3", len(final))
	}

	// The fresh numbers do go through, which is the second half of the
	// handshake: a 409 that could never be resolved would be a dead end.
	fresh := preview(t, c, models[:1])
	if fresh.Versions != 1 || fresh.Files != 2 {
		t.Fatalf("fresh preview = %+v, want 1 version and 2 files", fresh)
	}
	mustBulk(t, c, "delete", fmt.Sprintf(`{"modelIds":%s,"expectVersions":%d,"expectFiles":%d}`,
		idList(models[:1]), fresh.Versions, fresh.Files))
}

// The shape of the request is judged before anything else, so a selection the
// UI could not have produced is refused rather than run.
func TestBulkRefusesAnImpossibleSelection(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")
	tag := createTag(t, c, "printed")

	tooMany := make([]int64, library.MaxBulkModels+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}

	for _, tc := range []struct{ name, body string }{
		{"empty", fmt.Sprintf(`{"modelIds":[],"tagIds":[%d]}`, tag)},
		{"no tags", `{"modelIds":[1],"tagIds":[]}`},
		{"too many models", fmt.Sprintf(`{"modelIds":%s,"tagIds":[%d]}`, idList(tooMany), tag)},
	} {
		resp, body := bulk(t, c, "tags", tc.body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422: %s", tc.name, resp.StatusCode, body)
		}
	}
}

// Bulk delete's addressable set is roots, and that is a deadlock fix rather
// than a tidiness rule. If a version could be named directly, two deletes could
// take a version and a root in opposite orders across the two lock phases:
// [parent, other] locks parent then other in phase one and the version in phase
// two, while [version, other] locks the version in phase one and waits on
// other - a cycle, and a 500 for whichever side Postgres aborts. Refusing the
// version in phase one is what keeps phase-one locks all roots and phase-two
// locks all versions, and two disjoint sets cannot form a cycle.
//
// A weaker version of this test would delete the root and check the version
// went with it, which passes with or without the predicate.
func TestBulkDeleteRefusesAVersion(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "owner@example.com")

	made := three(t, c)
	root, version, other := made[0], made[1], made[2]
	mustAttach(t, c, version, &root)

	// The preview refuses first, so the confirmation never gets a number for a
	// selection the delete would not honour.
	body := fmt.Sprintf(`{"modelIds":[%d,%d]}`, version, other)
	if resp, got := bulk(t, c, "delete-preview", body); resp.StatusCode != http.StatusNotFound {
		t.Errorf("preview: got %d, want 404: %s", resp.StatusCode, got)
	}

	del := fmt.Sprintf(`{"modelIds":[%d,%d],"expectVersions":0,"expectFiles":0}`, version, other)
	if resp, got := bulk(t, c, "delete", del); resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete: got %d, want 404: %s", resp.StatusCode, got)
	}

	// And nothing went, including the unrelated root named alongside it.
	for _, id := range []int64{root, version, other} {
		if resp, got := c.get(fmt.Sprintf("/api/models/%d", id)); resp.StatusCode != http.StatusOK {
			t.Errorf("model %d: got %d, want it still there: %s", id, resp.StatusCode, got)
		}
	}
}

// A static segment under a parameter route is a routing question, not a
// compile-time one: /api/models/bulk/tags and /api/models/{id}/files are the
// same shape to a router that resolves parameters first, and the failure is a
// 422 about a model id of "bulk" rather than anything obviously about routing.
func TestBulkRoutesDoNotShadowTheModelRoutes(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	models := three(t, c)
	tag := createTag(t, c, "printed")

	mustBulk(t, c, "tags", fmt.Sprintf(`{"modelIds":[%d],"tagIds":[%d]}`, models[0], tag))

	// The per-model routes still answer, including the two-segment one that
	// looks most like the bulk paths.
	if resp, body := c.get(fmt.Sprintf("/api/models/%d", models[0])); resp.StatusCode != http.StatusOK {
		t.Errorf("GET a model: got %d: %s", resp.StatusCode, body)
	}
	ct, part := filePart(t, "extra.stl", "solid extra")
	if resp, body := c.addFile(models[0], ct, part); resp.StatusCode != http.StatusCreated {
		t.Errorf("POST a file: got %d: %s", resp.StatusCode, body)
	}
}

// Two bulk deletes over overlapping selections take their locks in the same
// order, so one waits for the other instead of both aborting on a deadlock.
//
// Forced rather than hoped for: a third connection holds the lowest id both
// requests want, both queue behind it, and only then is it released. Without the
// ORDER BY the two would each hold one of the two rows and want the other.
func TestConcurrentBulkDeletesDoNotDeadlock(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "owner@example.com")

	models := three(t, c)
	low, high := min(models[0], models[1]), max(models[0], models[1])

	ctx := context.Background()
	holder, err := db.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("holder connect: %v", err)
	}
	defer holder.Close()
	tx, err := holder.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	var locked int64
	if err := tx.QueryRow(ctx,
		"SELECT id FROM models WHERE id = $1 FOR UPDATE", low).Scan(&locked); err != nil {
		t.Fatalf("lock the low row: %v", err)
	}
	var holderPID int32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
		t.Fatalf("read the holder's pid: %v", err)
	}

	// Opposite orders in the request, which is what the UI would send if the
	// user selected the same two models from two directions.
	orders := [][]int64{{low, high}, {high, low}}
	codes := make(chan int, 2)
	for _, order := range orders {
		go func() {
			resp, _ := bulk(t, c, "delete",
				fmt.Sprintf(`{"modelIds":%s,"expectVersions":0,"expectFiles":2}`, idList(order)))
			codes <- resp.StatusCode
		}()
	}

	waitForBlockedBackends(t, dbURL, holderPID, 2)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}

	deleted, refused := 0, 0
	for range 2 {
		switch code := <-codes; code {
		case http.StatusNoContent:
			deleted++
		case http.StatusNotFound:
			refused++
		default:
			t.Errorf("unexpected status %d - a deadlock surfaces as a 500", code)
		}
	}
	if deleted != 1 || refused != 1 {
		t.Errorf("got %d deleted and %d refused, want one of each", deleted, refused)
	}

	// The loser must not have taken any blobs with it on the way out.
	final, _, _ := blobs(t, dir)
	if len(final) != 1 {
		t.Errorf("got %d blobs, want 1 - only the third model's should remain", len(final))
	}
}

// The other order that has to match: attaching a version locks the child and
// then the foreign key takes KEY SHARE on the parent, while a bulk delete locks
// its whole selection by ascending id. Select a parent and its future child in
// one delete, attach them in another tab, and those are the same two rows taken
// in opposite orders - a deadlock, and a 500 for whichever side Postgres picks.
// Only bulk delete reaches it: single-model delete never holds two roots.
//
// SetParent therefore locks through lockModels, like every other path that
// holds more than one model. Forced rather than hoped for: a third connection
// holds the row both requests want, the delete queues on it first, the attach
// queues second, and only then is it released. Postgres grants that lock in
// arrival order, so the delete is the one that ends up holding a row the attach
// still needs.
//
// A weaker version of this test would start both requests at once and pass
// because the two almost never interleave that way on their own.
func TestAttachDuringABulkDeleteDoesNotDeadlock(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	made := three(t, c)
	low, high := min(made[0], made[1]), max(made[0], made[1])
	want := preview(t, c, []int64{low, high})

	release, holderPID := hold(t, dbURL, low)
	defer release()

	codes := make(chan int, 2)
	// The delete queues first, so it is the one holding low when the attach
	// needs it.
	go report(codes, func() int {
		resp, _ := bulk(t, c, "delete", fmt.Sprintf(
			`{"modelIds":%s,"expectVersions":%d,"expectFiles":%d}`,
			idList([]int64{low, high}), want.Versions, want.Files))
		return resp.StatusCode
	})
	waitForBlockedBackends(t, dbURL, holderPID, 1)

	// The attach names the high id as the child, so without lockModels its own
	// order is high-then-low: the inversion.
	go report(codes, func() int {
		resp, _ := attach(t, c, high, &low)
		return resp.StatusCode
	})
	waitForBlockedBackends(t, dbURL, holderPID, 2)
	release()

	expectNoDeadlock(t, codes)
}

// The same inversion one level down, and the one sorting by id does not fix.
// Reparenting version C from root P to root Q sorts as C-then-Q when C's id is
// the lower, while a bulk delete of [P, Q] holds both roots and only then goes
// looking for their versions. lockModels takes roots before versions for this.
func TestReparentDuringABulkDeleteDoesNotDeadlock(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	made := three(t, c)
	parent, version, other := made[0], made[1], made[2]
	if !(version < other) {
		t.Fatalf("this test needs the version to sort below the new parent, got %v", made)
	}
	mustAttach(t, c, version, &parent)
	want := preview(t, c, []int64{parent, other})

	release, holderPID := hold(t, dbURL, other)
	defer release()

	codes := make(chan int, 2)
	go report(codes, func() int {
		resp, _ := bulk(t, c, "delete", fmt.Sprintf(
			`{"modelIds":%s,"expectVersions":%d,"expectFiles":%d}`,
			idList([]int64{parent, other}), want.Versions, want.Files))
		return resp.StatusCode
	})
	waitForBlockedBackends(t, dbURL, holderPID, 1)

	go report(codes, func() int {
		resp, _ := attach(t, c, version, &other)
		return resp.StatusCode
	})
	waitForBlockedBackends(t, dbURL, holderPID, 2)
	release()

	expectNoDeadlock(t, codes)
}

// The hole the two phases leave, and the reason lockModels counts. A version
// detached between the root statement and the version statement is a root by
// the time the second one looks, so neither locks it - and then the UPDATE that
// follows waits for a row somebody else is holding, in the order the phases
// exist to avoid.
//
// Forced, three deep. C is a version of P and Q is a root. A holder parks on Q.
// The reparent of C onto Q queues on Q having locked nothing, because C is not
// a root yet. C is then detached, which is what changes its shape. A bulk delete
// of [C, Q] queues second, taking C - now a root - on the way past. Releasing Q
// hands it to the reparent, whose version statement now finds nothing, and
// without the count it would go on to write C and deadlock against the delete.
//
// A weaker version of this test would leave out the detach and pass, because
// the phases only disagree when something changes between them.
func TestReparentRefusesAModelThatChangedShapeMidLock(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "owner@example.com")

	made := three(t, c)
	parent, version, other := made[0], made[1], made[2]
	mustAttach(t, c, version, &parent)

	release, holderPID := hold(t, dbURL, other)
	defer release()

	codes := make(chan int, 2)
	go report(codes, func() int {
		resp, _ := attach(t, c, version, &other)
		return resp.StatusCode
	})
	waitForBlockedBackends(t, dbURL, holderPID, 1)

	// The shape change, while the reparent is parked between its two phases.
	mustAttach(t, c, version, nil)
	want := preview(t, c, []int64{version, other})

	go report(codes, func() int {
		resp, _ := bulk(t, c, "delete", fmt.Sprintf(
			`{"modelIds":%s,"expectVersions":%d,"expectFiles":%d}`,
			idList([]int64{version, other}), want.Versions, want.Files))
		return resp.StatusCode
	})
	waitForBlockedBackends(t, dbURL, holderPID, 2)
	release()

	expectNoDeadlock(t, codes)
}

// The non-delete actions have the same problem and it is not hypothetical: they
// accept versions, so recategorizing [C, Q] where C is a version and Q a root
// takes C then Q on id order alone, while a bulk delete of [P, Q] holds both
// roots before it looks for C. They lock through lockModels for that reason,
// even though their own statements would take these rows anyway - a foreign
// key's key-share locks and an UPDATE's row locks land in whatever order the
// executor picked, which is not an order anyone chose.
//
// Unlike the two tests above, this one cannot force the failure: the order an
// UPDATE or a foreign key takes its row locks in is the executor's, and dropping
// lockModels from the category path made this deadlock every run one afternoon
// and no run the next. That is the argument for the lock rather than against
// the test - an order nobody chose is an order that can change under you - but
// it means this is a regression guard over a real interleaving, not a pin.
func TestBulkActionsDuringABulkDeleteDoNotDeadlock(t *testing.T) {
	for _, tc := range []struct {
		action string
		body   func(t *testing.T, c *client, ids []int64) string
	}{
		{"category", func(t *testing.T, c *client, ids []int64) string {
			cat := decodeCategory(t, mustCreate(t, c, "/api/categories",
				`{"name":"Prints","color":"#2563eb"}`)).ID
			return fmt.Sprintf(`{"modelIds":%s,"categoryId":%d}`, idList(ids), cat)
		}},
		{"tags", func(t *testing.T, c *client, ids []int64) string {
			return fmt.Sprintf(`{"modelIds":%s,"tagIds":[%d]}`,
				idList(ids), createTag(t, c, "resin"))
		}},
		{"collection", func(t *testing.T, c *client, ids []int64) string {
			return fmt.Sprintf(`{"modelIds":%s,"collectionId":%d}`,
				idList(ids), createCollection(t, c, "Queue", ""))
		}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			dbURL := testDatabase(t)
			pool := testPool(t, dbURL)
			ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
			c := signIn(t, ts, "owner@example.com")

			made := three(t, c)
			parent, version, other := made[0], made[1], made[2]
			mustAttach(t, c, version, &parent)
			body := tc.body(t, c, []int64{version, other})
			want := preview(t, c, []int64{parent, other})

			release, holderPID := hold(t, dbURL, other)
			defer release()

			codes := make(chan int, 2)
			go report(codes, func() int {
				resp, _ := bulk(t, c, "delete", fmt.Sprintf(
					`{"modelIds":%s,"expectVersions":%d,"expectFiles":%d}`,
					idList([]int64{parent, other}), want.Versions, want.Files))
				return resp.StatusCode
			})
			waitForBlockedBackends(t, dbURL, holderPID, 1)

			go report(codes, func() int {
				resp, _ := bulk(t, c, tc.action, body)
				return resp.StatusCode
			})
			waitForBlockedBackends(t, dbURL, holderPID, 2)
			release()

			expectNoDeadlock(t, codes)
		})
	}
}

// hold parks a transaction on one model row and reports the backend holding it,
// so waitForBlockedBackends can tell when the requests under test have queued
// behind it. The returned release is safe to call twice, so callers can defer it
// and still release at the point the test is about to make its assertion.
func hold(t *testing.T, dbURL string, id int64) (release func(), pid int32) {
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
		"SELECT id FROM models WHERE id = $1 FOR UPDATE", id).Scan(&locked); err != nil {
		t.Fatalf("hold %d: %v", id, err)
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

// report always sends, including when the request helper calls t.Fatalf and
// unwinds this goroutine, so a failing helper fails the test instead of leaving
// it blocked on a receive that never comes.
func report(codes chan<- int, do func() int) {
	code := 0
	defer func() { codes <- code }()
	code = do()
}

// Either order is a legal outcome. The delete can win and the attach then finds
// nothing to attach to; the attach can win and the delete then finds a version
// it was not told to expect. What is not legal is a 500, which is how a
// deadlock surfaces.
func expectNoDeadlock(t *testing.T, codes <-chan int) {
	t.Helper()
	for range 2 {
		switch code := <-codes; code {
		case http.StatusNoContent, http.StatusNotFound, http.StatusConflict,
			http.StatusUnprocessableEntity:
		default:
			t.Errorf("unexpected status %d - a deadlock surfaces as a 500", code)
		}
	}
}

// M9's race, in bulk. A version attached while the delete is waiting for the
// root's lock is invisible to the statement that took that lock, because its
// snapshot predates the wait - so the version's rows go with the cascade and its
// blobs are stranded. The second statement is what sees it, and only because it
// is a second statement.
//
// Mutation-pinned: fold the two SELECTs in BulkDelete into one and this fails
// with an orphaned blob.
func TestBulkDeleteSeesAVersionAttachedDuringTheLockWait(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "owner@example.com")

	models := three(t, c)
	root, newcomer := models[0], models[1]

	ctx := context.Background()
	holder, err := db.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("holder connect: %v", err)
	}
	defer holder.Close()
	tx, err := holder.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	var locked int64
	if err := tx.QueryRow(ctx,
		"SELECT id FROM models WHERE id = $1 FOR UPDATE", root).Scan(&locked); err != nil {
		t.Fatalf("lock the root: %v", err)
	}
	var holderPID int32
	if err := tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
		t.Fatalf("read the holder's pid: %v", err)
	}

	// The delete asks for the root alone and expects no versions, which is what
	// the preview said a moment ago.
	code := make(chan int, 1)
	go func() {
		resp, _ := bulk(t, c, "delete",
			fmt.Sprintf(`{"modelIds":[%d],"expectVersions":0,"expectFiles":1}`, root))
		code <- resp.StatusCode
	}()
	waitForBlockedBackends(t, dbURL, holderPID, 1)

	// Now attach, from the same connection that holds the lock, and commit. The
	// delete is still waiting, and its snapshot is older than this.
	// parent_id only: root_id is generated from it, and Postgres refuses to
	// write a generated column - which is the same reason M10 could not make an
	// FK to models(root_id) work.
	if _, err := tx.Exec(ctx,
		"UPDATE models SET parent_id = $1 WHERE id = $2", root, newcomer); err != nil {
		t.Fatalf("attach the newcomer: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the attach: %v", err)
	}

	// The handshake catches it: the delete now finds one version where the
	// caller was promised none, so it refuses rather than destroying a model
	// nobody agreed to.
	if got := <-code; got != http.StatusConflict {
		t.Fatalf("got %d, want 409 - the second statement did not see the new version", got)
	}

	// Both models still there, and every blob with them.
	for _, id := range []int64{root, newcomer} {
		if resp, _ := c.get(fmt.Sprintf("/api/models/%d", id)); resp.StatusCode != http.StatusOK {
			t.Errorf("model %d is gone after a refused delete", id)
		}
	}
	final, _, temp := blobs(t, dir)
	if len(final) != 3 || len(temp) != 0 {
		t.Errorf("got %d blobs and %d temp files, want 3 and 0", len(final), len(temp))
	}

	// And the fresh numbers go through, taking the newcomer with them.
	fresh := preview(t, c, []int64{root})
	mustBulk(t, c, "delete", fmt.Sprintf(`{"modelIds":[%d],"expectVersions":%d,"expectFiles":%d}`,
		root, fresh.Versions, fresh.Files))
	final, _, _ = blobs(t, dir)
	if len(final) != 1 {
		t.Errorf("got %d blobs after the real delete, want 1", len(final))
	}
}

// Bulk actions are signed-in operations, and an anonymous caller must not learn
// whether an id exists by watching the status code change.
func TestBulkActionsNeedAUser(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	anon := &client{t: t, ts: ts, hc: ts.Client()}

	// A body each, and a valid one: huma validates the schema before the handler
	// runs, so a shared superset body would be refused as malformed and the test
	// would report a 422 that says nothing about authentication.
	for _, tc := range []struct{ action, body string }{
		{"tags", `{"modelIds":[1],"tagIds":[1]}`},
		{"category", `{"modelIds":[1],"categoryId":1}`},
		{"collection", `{"modelIds":[1],"collectionId":1}`},
		{"delete-preview", `{"modelIds":[1]}`},
		{"delete", `{"modelIds":[1],"expectVersions":0,"expectFiles":0}`},
	} {
		resp, body := bulk(t, anon, tc.action, tc.body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: got %d, want 401: %s", tc.action, resp.StatusCode, body)
		}
	}
}
