package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/robert-crandall/3d-library/internal/library"
)

// Collections are a bag of models, not a container of them, and almost every
// test here is about that difference: what survives a delete, what a count
// includes, and what a membership row is allowed to outlive.

func createCollection(t *testing.T, c *client, name, description string) int64 {
	t.Helper()
	resp, body := c.send(http.MethodPost, "/api/collections",
		fmt.Sprintf(`{"name":%q,"description":%q}`, name, description))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create collection %q: got %d: %s", name, resp.StatusCode, body)
	}
	return decodeCollection(t, body).ID
}

func decodeCollection(t *testing.T, body string) library.CollectionSummary {
	t.Helper()
	var out library.CollectionSummary
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode collection: %v (body %q)", err, body)
	}
	return out
}

func listCollections(t *testing.T, c *client) []library.CollectionSummary {
	t.Helper()
	var out []library.CollectionSummary
	if err := json.Unmarshal([]byte(mustGet(t, c, "/api/collections")), &out); err != nil {
		t.Fatalf("decode collections: %v", err)
	}
	return out
}

// collectionCount is the number the sidebar shows beside a collection and the
// number its delete confirmation says out loud - the same value, read the same
// way, which is why the tests below never compute it themselves.
func collectionCount(t *testing.T, c *client, id int64) int {
	t.Helper()
	for _, coll := range listCollections(t, c) {
		if coll.ID == id {
			return coll.ModelCount
		}
	}
	t.Fatalf("collection %d is not in the list", id)
	return 0
}

func addToCollection(t *testing.T, c *client, modelID, collectionID int64) (*http.Response, string) {
	t.Helper()
	return c.send(http.MethodPut, fmt.Sprintf("/api/models/%d/collections/%d", modelID, collectionID), "")
}

func mustAddToCollection(t *testing.T, c *client, modelID, collectionID int64) {
	t.Helper()
	resp, body := addToCollection(t, c, modelID, collectionID)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("add %d to %d: got %d, want 204: %s", modelID, collectionID, resp.StatusCode, body)
	}
}

func collectionNames(m library.ModelDetail) []string {
	names := make([]string, 0, len(m.Collections))
	for _, coll := range m.Collections {
		names = append(names, coll.Name)
	}
	return names
}

// The lifecycle in one test: create, rename, and delete, with the count moving
// under it.
//
// One test rather than three because the interesting part is that the count
// travels with the collection through a rename - a rename that returned a stale
// or zero count would pass a create-only and a delete-only test, and the delete
// confirmation reads that number.
func TestCollectionLifecycle(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "collections@example.com")

	resp, body := c.send(http.MethodPost, "/api/collections",
		`{"name":"Voron 0.2 build","description":"The printer in the garage"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d: %s", resp.StatusCode, body)
	}
	created := decodeCollection(t, body)
	if created.Name != "Voron 0.2 build" || created.Description != "The printer in the garage" {
		t.Errorf("create returned %+v, want the name and description that went in", created)
	}
	if created.ModelCount != 0 {
		t.Errorf("a new collection holds %d models, want 0", created.ModelCount)
	}

	_, body = c.upload("Gantry", map[string]string{"g.stl": "solid gantry"})
	gantry := decodeModel(t, body)
	mustAddToCollection(t, c, gantry.ID, created.ID)

	resp, body = c.send(http.MethodPut, fmt.Sprintf("/api/collections/%d", created.ID),
		`{"name":"Voron 0.2","description":""}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename: got %d: %s", resp.StatusCode, body)
	}
	renamed := decodeCollection(t, body)
	if renamed.Name != "Voron 0.2" {
		t.Errorf("rename returned name %q, want %q", renamed.Name, "Voron 0.2")
	}
	// The description is optional, so clearing it has to be expressible.
	if renamed.Description != "" {
		t.Errorf("rename left description %q, want it cleared", renamed.Description)
	}
	// The rename response carries the count, and the delete confirmation is
	// what reads it. A rename that reported 0 here would still rename.
	if renamed.ModelCount != 1 {
		t.Errorf("rename reported %d models, want 1", renamed.ModelCount)
	}

	resp, body = c.send(http.MethodDelete, fmt.Sprintf("/api/collections/%d", created.ID), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d: %s", resp.StatusCode, body)
	}
	if got := listCollections(t, c); len(got) != 0 {
		t.Errorf("after delete the list has %d collections, want none", len(got))
	}
}

// A duplicate name is refused and writes nothing, ignoring case.
func TestDuplicateCollectionNameWritesNothing(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "dupe@example.com")

	createCollection(t, c, "Gifts 2026", "")

	resp, body := c.send(http.MethodPost, "/api/collections", `{"name":"gifts 2026","description":"a second one"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate create: got %d, want 422: %s", resp.StatusCode, body)
	}
	// The message is what the inline error renders, so it has to be a sentence
	// about collections rather than about a constraint.
	if !strings.Contains(body, "already exists") {
		t.Errorf("duplicate message = %s, want it to say the collection already exists", body)
	}
	// The point of the test: refusing is not enough, it has to not have written.
	if got := listCollections(t, c); len(got) != 1 {
		t.Errorf("after a refused duplicate there are %d collections, want 1", len(got))
	}
}

// Membership: adding twice is one row, a model can be in several collections,
// and removing leaves the model in the library.
//
// These are one test because they are one invariant seen from three sides - the
// join row is the only thing that changes, and the model never does.
func TestCollectionMembership(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "membership@example.com")

	_, body := c.upload("Idler", map[string]string{"idler.stl": "solid idler"})
	idler := decodeModel(t, body)
	// A model that has never been in a collection sends an empty list, not
	// null: the client renders a panel from a length.
	if idler.Collections == nil {
		t.Fatalf("a new model's collections = nil, want an empty list")
	}

	voron := createCollection(t, c, "Voron 0.2 build", "")
	gifts := createCollection(t, c, "Gifts 2026", "")

	mustAddToCollection(t, c, idler.ID, voron)
	// Adding the same model again succeeds and changes nothing. A second row
	// would double the count, which is the failure this is really watching for.
	mustAddToCollection(t, c, idler.ID, voron)
	if got := collectionCount(t, c, voron); got != 1 {
		t.Errorf("after adding twice the count is %d, want 1", got)
	}

	mustAddToCollection(t, c, idler.ID, gifts)
	if got := collectionCount(t, c, voron); got != 1 {
		t.Errorf("Voron count = %d after the model joined a second collection, want 1", got)
	}
	if got := collectionCount(t, c, gifts); got != 1 {
		t.Errorf("Gifts count = %d, want 1", got)
	}

	detail := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", idler.ID)))
	if got := collectionNames(detail); !equalStrings(got, []string{"Gifts 2026", "Voron 0.2 build"}) {
		t.Errorf("model's collections = %v, want both by name", got)
	}

	resp, body := c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d/collections/%d", idler.ID, voron), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove: got %d: %s", resp.StatusCode, body)
	}
	if got := collectionCount(t, c, voron); got != 0 {
		t.Errorf("after removing, Voron count = %d, want 0", got)
	}
	if got := collectionCount(t, c, gifts); got != 1 {
		t.Errorf("removing from one collection changed the other: Gifts = %d, want 1", got)
	}

	// The model is untouched: still in the library, still has its file. This is
	// acceptance criterion 4, and it is the reason removal is a join-row delete
	// and nothing else.
	still := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", idler.ID)))
	if still.FileCount != 1 {
		t.Errorf("after removal the model has %d files, want 1", still.FileCount)
	}
	if got := collectionNames(still); !equalStrings(got, []string{"Gifts 2026"}) {
		t.Errorf("after removal the model lists %v, want only Gifts 2026", got)
	}
	if got := listTotal(t, c, ""); got != 1 {
		t.Errorf("the library holds %d models after a removal, want 1", got)
	}

	// Removing a membership that is not there is a 404, so a page showing a
	// stale chip cannot report success for a write that did nothing.
	resp, _ = c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d/collections/%d", idler.ID, voron), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("removing a membership twice: got %d, want 404", resp.StatusCode)
	}
}

// Deleting a collection keeps every model in it, and their files on disk.
//
// The filesystem assertion is the point. A cascade that reached models would
// still leave the API answering for a moment, and only the blobs would say what
// really happened.
func TestDeletingACollectionKeepsItsModels(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "keep@example.com")

	_, body := c.upload("Panel", map[string]string{"panel.stl": "solid panel"})
	panel := decodeModel(t, body)
	_, body = c.upload("Door", map[string]string{"door.stl": "solid door"})
	door := decodeModel(t, body)

	id := createCollection(t, c, "Garage overhaul", "Everything for the garage")
	mustAddToCollection(t, c, panel.ID, id)
	mustAddToCollection(t, c, door.ID, id)

	before, _, _ := blobs(t, dir)
	if len(before) != 2 {
		t.Fatalf("before delete there are %d blobs, want 2", len(before))
	}
	// The count the confirmation quotes, read the way the UI reads it.
	if got := collectionCount(t, c, id); got != 2 {
		t.Fatalf("the collection holds %d models, want 2", got)
	}

	resp, body := c.send(http.MethodDelete, fmt.Sprintf("/api/collections/%d", id), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d: %s", resp.StatusCode, body)
	}

	if got := listTotal(t, c, ""); got != 2 {
		t.Errorf("after deleting the collection the library holds %d models, want 2", got)
	}
	after, _, _ := blobs(t, dir)
	if len(after) != 2 {
		t.Errorf("after deleting the collection there are %d blobs, want 2", len(after))
	}
	for _, id := range []int64{panel.ID, door.ID} {
		m := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", id)))
		if m.FileCount != 1 {
			t.Errorf("model %d has %d files after the collection went, want 1", id, m.FileCount)
		}
		if len(m.Collections) != 0 {
			t.Errorf("model %d still lists %v, want nothing", id, collectionNames(m))
		}
	}
}

// The collection view: filtering the grid by a collection returns exactly its
// models, and combines with search rather than replacing it.
func TestTheCollectionView(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "view@example.com")

	_, body := c.upload("Voron gantry", map[string]string{"a.stl": "solid a"})
	gantry := decodeModel(t, body)
	_, body = c.upload("Voron bed", map[string]string{"b.stl": "solid b"})
	bed := decodeModel(t, body)
	c.upload("Unrelated vase", map[string]string{"c.stl": "solid c"})

	id := createCollection(t, c, "Voron 0.2 build", "")
	mustAddToCollection(t, c, gantry.ID, id)
	mustAddToCollection(t, c, bed.ID, id)

	query := fmt.Sprintf("?collectionId=%d", id)
	if got := listTotal(t, c, query); got != 2 {
		t.Errorf("the collection view holds %d models, want 2", got)
	}
	// An empty collection is an empty page, not an error - that is what the
	// grid's own empty state is for.
	empty := createCollection(t, c, "Gifts 2026", "")
	if got := listTotal(t, c, fmt.Sprintf("?collectionId=%d", empty)); got != 0 {
		t.Errorf("an empty collection view holds %d models, want 0", got)
	}
	// The filters AND, they do not replace each other. A collection filter that
	// ignored the search would still pass a filter-only test.
	if got := listTotal(t, c, query+"&q=gantry"); got != 1 {
		t.Errorf("searching inside a collection found %d, want 1", got)
	}
	if got := listTotal(t, c, query+"&q=vase"); got != 0 {
		t.Errorf("a search that matches nothing in the collection found %d, want 0", got)
	}
}

// A version in a collection is in it, and is still not counted.
//
// This is the whole of the roots-only decision, from both ends: membership is
// not restricted, because restricting it in the database would stop a model
// that is in a collection from ever becoming a version. The rule lives in the
// count and the view instead, exactly as it does for tags.
//
// The attach and detach transitions are here rather than a steady state,
// because a count that filtered on parent_id only when the row was written
// would pass every static assertion and drift the moment anything moved.
func TestCollectionCountsAreRootsOnly(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "roots@example.com")

	_, body := c.upload("Bracket v1", map[string]string{"v1.stl": "solid one"})
	v1 := decodeModel(t, body)
	_, body = c.upload("Bracket v2", map[string]string{"v2.stl": "solid two"})
	v2 := decodeModel(t, body)

	id := createCollection(t, c, "Voron 0.2 build", "")
	mustAddToCollection(t, c, v1.ID, id)
	mustAddToCollection(t, c, v2.ID, id)
	if got := collectionCount(t, c, id); got != 2 {
		t.Fatalf("two roots in a collection count %d, want 2", got)
	}

	// Attaching v2 as a version of v1 takes it out of the grid, so it comes out
	// of the count and the view too - the same thing that happens to its tags.
	mustAttach(t, c, v2.ID, &v1.ID)
	if got := collectionCount(t, c, id); got != 1 {
		t.Errorf("after attaching, the count is %d, want 1", got)
	}
	if got := listTotal(t, c, fmt.Sprintf("?collectionId=%d", id)); got != 1 {
		t.Errorf("after attaching, the collection view holds %d, want 1", got)
	}
	// The membership itself survives, which is what makes the transition
	// reversible and the row removable from the version's own page.
	version := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", v2.ID)))
	if got := collectionNames(version); !equalStrings(got, []string{"Voron 0.2 build"}) {
		t.Errorf("a version's collections = %v, want the collection it was put in", got)
	}

	detached := (*int64)(nil)
	mustAttach(t, c, v2.ID, detached)
	if got := collectionCount(t, c, id); got != 2 {
		t.Errorf("after detaching, the count is %d, want 2 again", got)
	}
}

// Deleting a model takes its memberships with it and leaves the collection.
func TestDeletingAModelLeavesItsCollections(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "modeldelete@example.com")

	_, body := c.upload("Spool holder", map[string]string{"s.stl": "solid s"})
	holder := decodeModel(t, body)
	id := createCollection(t, c, "Voron 0.2 build", "")
	mustAddToCollection(t, c, holder.ID, id)

	resp, body := c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d", holder.ID), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete model: got %d: %s", resp.StatusCode, body)
	}

	// The collection is still there, holding nothing. A membership row that
	// outlived its model would show up here as a count of 1 over a model that
	// 404s - the join row cascades, which is why it does not.
	if got := collectionCount(t, c, id); got != 0 {
		t.Errorf("after deleting its only model the count is %d, want 0", got)
	}
	if got := listTotal(t, c, fmt.Sprintf("?collectionId=%d", id)); got != 0 {
		t.Errorf("the view holds %d models, want 0", got)
	}
}

// Everything about another user's rows is a 404, never a 403 and never a leak.
//
// Table-driven over the endpoints because they resolve ownership in four
// different statements - a list filter, an update, a delete and a membership
// CTE - and a mistake in any one of them is invisible from the others.
func TestAnotherUsersCollectionIsNotFound(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})

	owner := signIn(t, ts, "owner@example.com")
	_, body := owner.upload("Their model", map[string]string{"t.stl": "solid t"})
	theirModel := decodeModel(t, body)
	theirs := createCollection(t, owner, "Their collection", "")

	intruder := signIn(t, ts, "intruder@example.com")
	_, body = intruder.upload("My model", map[string]string{"m.stl": "solid m"})
	myModel := decodeModel(t, body)
	mine := createCollection(t, intruder, "My collection", "")

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"filtering the grid by it", http.MethodGet, fmt.Sprintf("/api/models?collectionId=%d", theirs), ""},
		{"filtering by one that never existed", http.MethodGet, "/api/models?collectionId=987654", ""},
		{"renaming it", http.MethodPut, fmt.Sprintf("/api/collections/%d", theirs), `{"name":"Taken","description":""}`},
		{"deleting it", http.MethodDelete, fmt.Sprintf("/api/collections/%d", theirs), ""},
		{"adding my model to it", http.MethodPut, fmt.Sprintf("/api/models/%d/collections/%d", myModel.ID, theirs), ""},
		{"adding their model to mine", http.MethodPut, fmt.Sprintf("/api/models/%d/collections/%d", theirModel.ID, mine), ""},
		{"removing their model from mine", http.MethodDelete, fmt.Sprintf("/api/models/%d/collections/%d", theirModel.ID, mine), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := intruder.send(tc.method, tc.path, tc.body)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("got %d, want 404: %s", resp.StatusCode, body)
			}
		})
	}

	// Nothing crossed over: the owner's collection is untouched and empty, and
	// the intruder's is empty too.
	if got := listCollections(t, owner); len(got) != 1 || got[0].Name != "Their collection" || got[0].ModelCount != 0 {
		t.Errorf("the owner's collections are %+v, want one untouched and empty", got)
	}
	if got := collectionCount(t, intruder, mine); got != 0 {
		t.Errorf("the intruder's collection holds %d models, want 0", got)
	}
}
