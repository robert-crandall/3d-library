package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robert-crandall/3d-library/internal/library"
)

// The taxonomy is three small tables and two join tables, so almost nothing
// here is about one handler in isolation. What is worth pinning is the seams:
// the unique index that has to hold when two requests race, the ownership check
// that has to refuse another user's id without leaking that it exists, the
// transaction that has to roll the name back when a tag id is wrong, and the
// counts, which read from three places at once and are the only thing the
// sidebar shows.

// createTaxonomy posts a name to one of the three collections and returns the
// created row.
func (c *client) createTaxonomy(path, body string) (*http.Response, string) {
	c.t.Helper()
	return c.send(http.MethodPost, path, body)
}

func decodeCategory(t *testing.T, body string) library.Category {
	t.Helper()
	var out library.Category
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode category: %v (body %q)", err, body)
	}
	return out
}

func decodeLabel(t *testing.T, body string) library.Label {
	t.Helper()
	var out library.Label
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode label: %v (body %q)", err, body)
	}
	return out
}

func decodeLabels(t *testing.T, body string) []library.LabelSummary {
	t.Helper()
	var out []library.LabelSummary
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode labels: %v (body %q)", err, body)
	}
	return out
}

func decodeCounts(t *testing.T, body string) library.Counts {
	t.Helper()
	var out library.Counts
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode counts: %v (body %q)", err, body)
	}
	return out
}

func decodeCategories(t *testing.T, body string) []library.CategorySummary {
	t.Helper()
	var out []library.CategorySummary
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode categories: %v (body %q)", err, body)
	}
	return out
}

// assign is the update body with taxonomy on it. Every field is required, so
// the callers below always say all three even when changing one.
func assign(name string, categoryID *int64, tagIDs, materialIDs []int64) string {
	if tagIDs == nil {
		tagIDs = []int64{}
	}
	if materialIDs == nil {
		materialIDs = []int64{}
	}
	body, err := json.Marshal(map[string]any{
		"name": name, "description": "", "printTips": "", "sourceUrl": "",
		"categoryId": categoryID, "tagIds": tagIDs, "materialIds": materialIDs,
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

// uploadModel is the two-line "get me a model to hang taxonomy off" the tests
// below all start with.
func uploadModel(c *client, name string) library.ModelDetail {
	c.t.Helper()
	resp, body := c.upload(name, map[string]string{"a.stl": "solid a"})
	if resp.StatusCode != http.StatusCreated {
		c.t.Fatalf("upload %s: got %d: %s", name, resp.StatusCode, body)
	}
	return decodeModel(c.t, body)
}

// Each of the three collections is its own table, its own handler and its own
// SQL, so "it works for categories" says nothing about tags. Running the same
// lifecycle over all three is the only thing that would catch a copy-paste that
// left one of them reading the wrong table.
func TestTaxonomyRoundTrips(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "taxonomist@example.com")

	for _, tc := range []struct {
		what, path, create, rename string
		// What the list must say afterwards. A rename that returns 200 and
		// writes nothing passes a test that only looks at the status code and
		// at whether the id is still there, so name the values instead.
		wantAfter []string
	}{
		{"category", "/api/categories",
			`{"name":"Functional","color":"#2563eb"}`,
			`{"name":"Functional parts","color":"#16a34a"}`,
			[]string{`"name":"Functional parts"`, `"color":"#16a34a"`}},
		{"tag", "/api/tags", `{"name":"benchy"}`, `{"name":"boat"}`,
			[]string{`"name":"boat"`}},
		{"material", "/api/materials", `{"name":"PCTG"}`, `{"name":"PC"}`,
			[]string{`"name":"PC"`}},
	} {
		resp, body := c.createTaxonomy(tc.path, tc.create)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%s create: got %d: %s", tc.what, resp.StatusCode, body)
		}
		var created struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(body), &created); err != nil {
			t.Fatalf("%s create decode: %v", tc.what, err)
		}
		if created.ID == 0 {
			t.Errorf("%s create returned no id: %s", tc.what, body)
		}

		one := fmt.Sprintf("%s/%d", tc.path, created.ID)
		resp, body = c.send(http.MethodPut, one, tc.rename)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s rename: got %d: %s", tc.what, resp.StatusCode, body)
		}

		resp, body = c.get(tc.path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s list: got %d: %s", tc.what, resp.StatusCode, body)
		}
		if !strings.Contains(body, `"id":`+fmt.Sprint(created.ID)) {
			t.Errorf("%s list is missing the row it just made: %s", tc.what, body)
		}
		for _, want := range tc.wantAfter {
			if !strings.Contains(body, want) {
				t.Errorf("%s list does not show %s after the rename: %s", tc.what, want, body)
			}
		}
		if strings.Contains(body, `"name":"`+created.Name+`"`) {
			t.Errorf("%s list still shows the old name %q: %s", tc.what, created.Name, body)
		}

		resp, body = c.send(http.MethodDelete, one, "")
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s delete: got %d: %s", tc.what, resp.StatusCode, body)
		}
		resp, body = c.send(http.MethodDelete, one, "")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s delete twice: got %d, want 404: %s", tc.what, resp.StatusCode, body)
		}
	}
}

// Everything is scoped by owner in SQL rather than by a check the handler could
// forget. 404 rather than 403 throughout, because 403 confirms the row exists.
func TestTaxonomyIsScopedToItsOwner(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	owner := signIn(t, ts, "owner@example.com")
	other := signIn(t, ts, "other@example.com")

	for _, tc := range []struct {
		what, path, create, rename string
	}{
		{"category", "/api/categories",
			`{"name":"Private","color":"#2563eb"}`,
			`{"name":"Stolen","color":"#000000"}`},
		{"tag", "/api/tags", `{"name":"private"}`, `{"name":"stolen"}`},
		{"material", "/api/materials", `{"name":"Private"}`, `{"name":"Stolen"}`},
	} {
		resp, body := owner.createTaxonomy(tc.path, tc.create)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%s create: got %d: %s", tc.what, resp.StatusCode, body)
		}
		var created struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal([]byte(body), &created); err != nil {
			t.Fatalf("%s decode: %v", tc.what, err)
		}
		one := fmt.Sprintf("%s/%d", tc.path, created.ID)

		for _, attempt := range []struct {
			method, body string
		}{
			{http.MethodPut, tc.rename},
			{http.MethodDelete, ""},
		} {
			resp, out := other.send(attempt.method, one, attempt.body)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s %s by a stranger: got %d, want 404: %s",
					attempt.method, tc.what, resp.StatusCode, out)
			}
		}

		// The list is the other half: a stranger's collection must not contain
		// it either. Materials are seeded, so this is not an emptiness check.
		_, out := other.get(tc.path)
		if strings.Contains(out, `"id":`+fmt.Sprint(created.ID)) {
			t.Errorf("%s list leaks another user's row: %s", tc.what, out)
		}

		// And the row survived every one of those attempts.
		_, out = owner.get(tc.path)
		if !strings.Contains(out, `"id":`+fmt.Sprint(created.ID)) {
			t.Errorf("%s: the owner's row did not survive: %s", tc.what, out)
		}
	}
}

// Names are unique per user, case-insensitively, and that is enforced by a
// unique index on lower(name) rather than by a SELECT before the INSERT. A
// pre-check would pass this test and still let two concurrent requests through;
// TestTaxonomyDuplicatesLoseARace is the half that measures it.
//
// The message matters as much as the status: huma renders err.Error() into
// errors[].message, so a wrapped pgconn error would put the index name and the
// SQL statement on the screen.
func TestTaxonomyRefusesDuplicateNames(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "dupes@example.com")

	for _, tc := range []struct {
		what, path, first, second string
	}{
		{"category", "/api/categories",
			`{"name":"Toys","color":"#2563eb"}`,
			`{"name":"toys","color":"#16a34a"}`},
		{"tag", "/api/tags", `{"name":"Benchy"}`, `{"name":"BENCHY"}`},
		{"material", "/api/materials", `{"name":"Nylon"}`, `{"name":"  nylon  "}`},
	} {
		resp, body := c.createTaxonomy(tc.path, tc.first)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("%s first: got %d: %s", tc.what, resp.StatusCode, body)
		}
		before := len(decodeLabels(t, mustGet(t, c, tc.path)))

		resp, body = c.createTaxonomy(tc.path, tc.second)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s second: got %d, want 422: %s", tc.what, resp.StatusCode, body)
		}
		for _, leak := range []string{"SQLSTATE", "23505", "duplicate key", "_lower_", "pgconn"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s duplicate error leaks %q: %s", tc.what, leak, body)
			}
		}
		if after := len(decodeLabels(t, mustGet(t, c, tc.path))); after != before {
			t.Errorf("%s: refusal still created a row: %d then %d", tc.what, before, after)
		}
	}
}

// The whole reason the uniqueness lives in the index and not in a SELECT: two
// requests that both check "does Toys exist" before either inserts would both
// see no, and application-level checking would let both through. This fails
// against a pre-check implementation and passes against the index.
func TestTaxonomyDuplicatesLoseARace(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "racer@example.com")

	const n = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		codes   []int
		bodies  []string
		start   = make(chan struct{})
		created = `{"name":"Racy"}`
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, body := c.send(http.MethodPost, "/api/tags", created)
			mu.Lock()
			codes = append(codes, resp.StatusCode)
			bodies = append(bodies, body)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	wins, refusals := 0, 0
	for i, code := range codes {
		switch code {
		case http.StatusCreated:
			wins++
		case http.StatusUnprocessableEntity:
			refusals++
		default:
			t.Errorf("unexpected %d: %s", code, bodies[i])
		}
	}
	if wins != 1 || refusals != n-1 {
		t.Errorf("got %d created and %d refused, want 1 and %d", wins, refusals, n-1)
	}
	if got := len(decodeLabels(t, mustGet(t, c, "/api/tags"))); got != 1 {
		t.Errorf("the race left %d tags, want 1", got)
	}
}

// Materials come seeded, and the trigger that seeds them fires on the users
// insert - which happens inside the foundation's registration, in a package
// this app cannot hook. Asserting on the names rather than the count would
// still pass if the trigger seeded five copies of PLA.
func TestNewUsersStartWithTheStandardMaterials(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "fresh@example.com")

	got := decodeLabels(t, mustGet(t, c, "/api/materials"))
	want := []string{"ABS", "ASA", "PETG", "PLA", "TPU"} // ordered by lower(name)
	if len(got) != len(want) {
		t.Fatalf("got %d materials, want %d: %v", len(got), len(want), got)
	}
	for i, m := range got {
		if m.Name != want[i] {
			t.Errorf("material %d is %q, want %q", i, m.Name, want[i])
		}
		if m.ModelCount != 0 {
			t.Errorf("%s starts with %d models, want 0", m.Name, m.ModelCount)
		}
	}

	// Seeded rows are the user's own, not shared: renaming one must not touch
	// anybody else's library.
	other := signIn(t, ts, "fresh2@example.com")
	resp, body := c.send(http.MethodPut,
		fmt.Sprintf("/api/materials/%d", got[0].ID), `{"name":"ABS-CF"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rename: got %d: %s", resp.StatusCode, body)
	}
	if names := decodeLabels(t, mustGet(t, other, "/api/materials")); names[0].Name != "ABS" {
		t.Errorf("another user's material changed to %q", names[0].Name)
	}
}

// Assignment rides the model PUT, and the PUT is a full replacement, so the
// round trip is the only thing that says the join tables were written rather
// than the response being echoed back.
func TestAssigningTaxonomyToAModel(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "assigner@example.com")

	model := uploadModel(c, "Bracket")
	path := fmt.Sprintf("/api/models/%d", model.ID)

	// A fresh upload has empty collections, not null ones: the UI iterates them
	// without a guard, so `"tags": null` is a runtime error on the detail page.
	fresh := mustGet(t, c, path)
	for _, want := range []string{`"tags":[]`, `"materials":[]`} {
		if !strings.Contains(strings.ReplaceAll(fresh, " ", ""), want) {
			t.Errorf("a fresh model should carry %s: %s", want, fresh)
		}
	}
	if strings.Contains(fresh, `"category"`) {
		t.Errorf("an uncategorized model should omit category: %s", fresh)
	}

	cat := decodeCategory(t, mustCreate(t, c, "/api/categories", `{"name":"Functional","color":"#2563eb"}`))
	tag := decodeLabel(t, mustCreate(t, c, "/api/tags", `{"name":"bracket"}`))
	other := decodeLabel(t, mustCreate(t, c, "/api/tags", `{"name":"shelf"}`))
	pla := decodeLabels(t, mustGet(t, c, "/api/materials"))[3] // PLA, ordered by name

	resp, body := c.send(http.MethodPut, path,
		assign("Bracket", &cat.ID, []int64{tag.ID, other.ID}, []int64{pla.ID}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assign: got %d: %s", resp.StatusCode, body)
	}

	got := decodeModel(t, mustGet(t, c, path))
	if got.Category == nil || got.Category.ID != cat.ID || got.Category.Color != "#2563eb" {
		t.Errorf("category did not stick: %+v", got.Category)
	}
	if len(got.Tags) != 2 || got.Tags[0].Name != "bracket" || got.Tags[1].Name != "shelf" {
		t.Errorf("tags did not stick: %+v", got.Tags)
	}
	if len(got.Materials) != 1 || got.Materials[0].Name != "PLA" {
		t.Errorf("materials did not stick: %+v", got.Materials)
	}

	// Saving the same thing twice is what the edit dialog does when the user
	// changes only the description. Replace-then-insert makes this fine;
	// insert-only would violate the join table's primary key on the second go.
	resp, body = c.send(http.MethodPut, path,
		assign("Bracket", &cat.ID, []int64{tag.ID, other.ID}, []int64{pla.ID}))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-save: got %d: %s", resp.StatusCode, body)
	}

	// A replacement drops what it does not name, and null uncategorizes.
	resp, body = c.send(http.MethodPut, path, assign("Bracket", nil, []int64{other.ID}, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("shrink: got %d: %s", resp.StatusCode, body)
	}
	got = decodeModel(t, mustGet(t, c, path))
	if got.Category != nil {
		t.Errorf("null should uncategorize, got %+v", got.Category)
	}
	if len(got.Tags) != 1 || got.Tags[0].ID != other.ID {
		t.Errorf("replacement did not drop the missing tag: %+v", got.Tags)
	}
	if len(got.Materials) != 0 {
		t.Errorf("replacement did not drop the materials: %+v", got.Materials)
	}
}

func mustCreate(t *testing.T, c *client, path, body string) string {
	t.Helper()
	resp, out := c.send(http.MethodPost, path, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("post %s: got %d: %s", path, resp.StatusCode, out)
	}
	return out
}

// The dangerous shape of a full-replacement PUT: naming somebody else's tag
// must not attach it, and must not half-apply the rest of the body either. A
// handler that updated the row and then failed on the join insert would leave
// the model renamed - so the assertion is on the name, not just the status.
func TestUpdateRefusesAnotherUsersTaxonomy(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	mine := signIn(t, ts, "mine@example.com")
	theirs := signIn(t, ts, "theirs@example.com")

	model := uploadModel(mine, "Original")
	path := fmt.Sprintf("/api/models/%d", model.ID)

	strangerCat := decodeCategory(t, mustCreate(t, theirs, "/api/categories",
		`{"name":"Theirs","color":"#2563eb"}`))
	strangerTag := decodeLabel(t, mustCreate(t, theirs, "/api/tags", `{"name":"theirs"}`))
	strangerMat := decodeLabel(t, mustCreate(t, theirs, "/api/materials", `{"name":"Theirs"}`))

	for _, tc := range []struct {
		what, body string
	}{
		{"a stranger's category", assign("Renamed", &strangerCat.ID, nil, nil)},
		{"a stranger's tag", assign("Renamed", nil, []int64{strangerTag.ID}, nil)},
		{"a stranger's material", assign("Renamed", nil, nil, []int64{strangerMat.ID})},
		{"a category that does not exist", assign("Renamed", ptr(int64(999999)), nil, nil)},
		{"a tag that does not exist", assign("Renamed", nil, []int64{999999}, nil)},
	} {
		resp, out := mine.send(http.MethodPut, path, tc.body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422: %s", tc.what, resp.StatusCode, out)
		}
		got := decodeModel(t, mustGet(t, mine, path))
		if got.Name != "Original" {
			t.Errorf("%s: the refusal still renamed the model to %q", tc.what, got.Name)
		}
		if got.Category != nil || len(got.Tags) != 0 || len(got.Materials) != 0 {
			t.Errorf("%s: the refusal still attached something: %+v", tc.what, got)
		}
	}

	// And the stranger's own rows are untouched by all of that.
	if got := decodeLabels(t, mustGet(t, theirs, "/api/tags")); len(got) != 1 || got[0].ModelCount != 0 {
		t.Errorf("the stranger's tag was changed: %+v", got)
	}
}

func ptr[T any](v T) *T { return &v }

// The three fields are required rather than optional-with-a-default, because
// optional means a client that forgets tagIds silently wipes the model's tags.
// This is the test that would fail if somebody "helpfully" relaxed them.
func TestUpdateRequiresEveryTaxonomyField(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "forgetful@example.com")

	model := uploadModel(c, "Careful")
	path := fmt.Sprintf("/api/models/%d", model.ID)
	tag := decodeLabel(t, mustCreate(t, c, "/api/tags", `{"name":"keepme"}`))

	resp, body := c.send(http.MethodPut, path, assign("Careful", nil, []int64{tag.ID}, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup: got %d: %s", resp.StatusCode, body)
	}

	for _, tc := range []struct {
		what, body string
	}{
		{"no tagIds", `{"name":"Careful","description":"","printTips":"","sourceUrl":"","categoryId":null,"materialIds":[]}`},
		{"no materialIds", `{"name":"Careful","description":"","printTips":"","sourceUrl":"","categoryId":null,"tagIds":[]}`},
		{"no categoryId", `{"name":"Careful","description":"","printTips":"","sourceUrl":"","tagIds":[],"materialIds":[]}`},
	} {
		resp, out := c.send(http.MethodPut, path, tc.body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422: %s", tc.what, resp.StatusCode, out)
		}
	}
	if got := decodeModel(t, mustGet(t, c, path)); len(got.Tags) != 1 {
		t.Errorf("a refused update still changed the tags: %+v", got.Tags)
	}
}

// Deleting a category leaves its models, uncategorized. That is ON DELETE SET
// NULL rather than a cascade, and getting it wrong deletes the user's models.
// Deleting a tag drops the join rows and nothing else.
func TestDeletingTaxonomyKeepsTheModels(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "deleter@example.com")

	model := uploadModel(c, "Survivor")
	path := fmt.Sprintf("/api/models/%d", model.ID)
	cat := decodeCategory(t, mustCreate(t, c, "/api/categories", `{"name":"Doomed","color":"#2563eb"}`))
	tag := decodeLabel(t, mustCreate(t, c, "/api/tags", `{"name":"doomed"}`))

	resp, body := c.send(http.MethodPut, path,
		assign("Survivor", &cat.ID, []int64{tag.ID}, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assign: got %d: %s", resp.StatusCode, body)
	}

	if resp, body := c.send(http.MethodDelete,
		fmt.Sprintf("/api/categories/%d", cat.ID), ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete category: got %d: %s", resp.StatusCode, body)
	}
	got := decodeModel(t, mustGet(t, c, path))
	if got.Category != nil {
		t.Errorf("the model kept a deleted category: %+v", got.Category)
	}
	if len(got.Tags) != 1 {
		t.Errorf("deleting a category touched the tags: %+v", got.Tags)
	}

	if resp, body := c.send(http.MethodDelete,
		fmt.Sprintf("/api/tags/%d", tag.ID), ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete tag: got %d: %s", resp.StatusCode, body)
	}
	got = decodeModel(t, mustGet(t, c, path))
	if len(got.Tags) != 0 {
		t.Errorf("the model kept a deleted tag: %+v", got.Tags)
	}

	// The other direction: deleting the model must not leave join rows behind
	// pointing at nothing, which would show up as an inflated count.
	if resp, body := c.send(http.MethodDelete, path, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete model: got %d: %s", resp.StatusCode, body)
	}
	counts := decodeCounts(t, mustGet(t, c, "/api/library/counts"))
	if counts.Models != 0 || counts.Uncategorized != 0 {
		t.Errorf("deleting the model left counts at %+v", counts)
	}
	for _, m := range decodeLabels(t, mustGet(t, c, "/api/materials")) {
		if m.ModelCount != 0 {
			t.Errorf("%s still counts %d models", m.Name, m.ModelCount)
		}
	}
}

// The sidebar is entirely counts, and they come from three queries against two
// join tables and one FK. Asserting the total alone would pass with every
// per-row count stuck at zero.
func TestCountsFollowAssignment(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "counter@example.com")

	one := uploadModel(c, "One")
	two := uploadModel(c, "Two")
	cat := decodeCategory(t, mustCreate(t, c, "/api/categories", `{"name":"Toys","color":"#2563eb"}`))
	tag := decodeLabel(t, mustCreate(t, c, "/api/tags", `{"name":"toy"}`))

	counts := decodeCounts(t, mustGet(t, c, "/api/library/counts"))
	if counts.Models != 2 || counts.Uncategorized != 2 {
		t.Fatalf("before assigning: %+v", counts)
	}

	for _, m := range []library.ModelDetail{one, two} {
		resp, body := c.send(http.MethodPut, fmt.Sprintf("/api/models/%d", m.ID),
			assign(m.Name, &cat.ID, []int64{tag.ID}, nil))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("assign %s: got %d: %s", m.Name, resp.StatusCode, body)
		}
	}

	counts = decodeCounts(t, mustGet(t, c, "/api/library/counts"))
	if counts.Models != 2 || counts.Uncategorized != 0 {
		t.Errorf("after assigning: %+v", counts)
	}
	cats := decodeCategories(t, mustGet(t, c, "/api/categories"))
	if len(cats) != 1 || cats[0].ModelCount != 2 {
		t.Errorf("category count: %+v", cats)
	}
	tags := decodeLabels(t, mustGet(t, c, "/api/tags"))
	if len(tags) != 1 || tags[0].ModelCount != 2 {
		t.Errorf("tag count: %+v", tags)
	}

	// Unassigning one moves it back to Uncategorized without deleting anything.
	resp, body := c.send(http.MethodPut, fmt.Sprintf("/api/models/%d", one.ID),
		assign("One", nil, nil, nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unassign: got %d: %s", resp.StatusCode, body)
	}
	counts = decodeCounts(t, mustGet(t, c, "/api/library/counts"))
	cats = decodeCategories(t, mustGet(t, c, "/api/categories"))
	tags = decodeLabels(t, mustGet(t, c, "/api/tags"))
	if counts.Uncategorized != 1 || cats[0].ModelCount != 1 || tags[0].ModelCount != 1 {
		t.Errorf("after unassigning one: %+v %+v %+v", counts, cats, tags)
	}
}

// Filtering is the sidebar's other half, and the two have to agree: a category
// that says 2 must list 2. They are separate SQL, so nothing but a test keeps
// them honest.
func TestFilteringMatchesTheCounts(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "filter@example.com")

	toys := decodeCategory(t, mustCreate(t, c, "/api/categories", `{"name":"Toys","color":"#2563eb"}`))
	tools := decodeCategory(t, mustCreate(t, c, "/api/categories", `{"name":"Tools","color":"#16a34a"}`))
	tag := decodeLabel(t, mustCreate(t, c, "/api/tags", `{"name":"shared"}`))

	for _, tc := range []struct {
		name string
		cat  *int64
		tags []int64
	}{
		{"Toy A", &toys.ID, []int64{tag.ID}},
		{"Toy B", &toys.ID, nil},
		{"Tool A", &tools.ID, []int64{tag.ID}},
		{"Loose", nil, nil},
	} {
		m := uploadModel(c, tc.name)
		resp, body := c.send(http.MethodPut, fmt.Sprintf("/api/models/%d", m.ID),
			assign(tc.name, tc.cat, tc.tags, nil))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("assign %s: got %d: %s", tc.name, resp.StatusCode, body)
		}
	}

	for _, tc := range []struct {
		what, query string
		want        []string
	}{
		{"everything", "", []string{"Loose", "Tool A", "Toy B", "Toy A"}},
		{"a category", fmt.Sprintf("?categoryId=%d", toys.ID), []string{"Toy B", "Toy A"}},
		{"a tag", fmt.Sprintf("?tagId=%d", tag.ID), []string{"Tool A", "Toy A"}},
		{"uncategorized", "?uncategorized=true", []string{"Loose"}},
		// Both at once is what clicking a tag while a category is selected
		// does. AND, not OR: the sidebar shows one selection per section.
		{"a category and a tag", fmt.Sprintf("?categoryId=%d&tagId=%d", toys.ID, tag.ID), []string{"Toy A"}},
		{"a category with nothing in it", fmt.Sprintf("?categoryId=%d&uncategorized=true", toys.ID), nil},
	} {
		var got []library.Model
		if err := json.Unmarshal([]byte(mustGet(t, c, "/api/models"+tc.query)), &got); err != nil {
			t.Fatalf("%s: decode: %v", tc.what, err)
		}
		names := make([]string, len(got))
		for i, m := range got {
			names[i] = m.Name
		}
		if fmt.Sprint(names) != fmt.Sprint(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.what, names, tc.want)
		}
	}

	// The filtered list still carries the category, because the grid draws a
	// colour square on every tile whether or not a filter is on.
	var filtered []library.Model
	if err := json.Unmarshal([]byte(mustGet(t, c,
		fmt.Sprintf("/api/models?categoryId=%d", toys.ID))), &filtered); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, m := range filtered {
		if m.Category == nil || m.Category.Name != "Toys" {
			t.Errorf("%s lost its category in the list: %+v", m.Name, m.Category)
		}
	}

	counts := decodeCounts(t, mustGet(t, c, "/api/library/counts"))
	byName := map[string]int{}
	for _, cat := range decodeCategories(t, mustGet(t, c, "/api/categories")) {
		byName[cat.Name] = cat.ModelCount
	}
	if byName["Toys"] != 2 || byName["Tools"] != 1 || counts.Uncategorized != 1 || counts.Models != 4 {
		t.Errorf("counts disagree with the filters: %+v %+v", counts, byName)
	}
}

// M1 left parent_id on models for milestone 9's one-level nesting, and its list
// query already hides children. Counts and filters have to hide them too, or
// the sidebar says 3 and the grid shows 2.
func TestCountsAndFiltersIgnoreChildModels(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "nester@example.com")

	parent := uploadModel(c, "Parent")
	child := uploadModel(c, "Child")
	cat := decodeCategory(t, mustCreate(t, c, "/api/categories", `{"name":"Sets","color":"#2563eb"}`))
	tag := decodeLabel(t, mustCreate(t, c, "/api/tags", `{"name":"set"}`))

	for _, m := range []library.ModelDetail{parent, child} {
		resp, body := c.send(http.MethodPut, fmt.Sprintf("/api/models/%d", m.ID),
			assign(m.Name, &cat.ID, []int64{tag.ID}, nil))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("assign %s: got %d: %s", m.Name, resp.StatusCode, body)
		}
	}

	// Milestone 9 owns the endpoint that does this; until then the column is
	// only reachable from SQL, which is also the honest way to set up a state
	// this app's own API cannot yet produce.
	if _, err := pool.Exec(t.Context(),
		"UPDATE models SET parent_id = $1 WHERE id = $2", parent.ID, child.ID); err != nil {
		t.Fatalf("nest: %v", err)
	}

	counts := decodeCounts(t, mustGet(t, c, "/api/library/counts"))
	if counts.Models != 1 || counts.Uncategorized != 0 {
		t.Errorf("counts see the child: %+v", counts)
	}
	if cats := decodeCategories(t, mustGet(t, c, "/api/categories")); cats[0].ModelCount != 1 {
		t.Errorf("the category counts the child: %+v", cats)
	}
	if tags := decodeLabels(t, mustGet(t, c, "/api/tags")); tags[0].ModelCount != 1 {
		t.Errorf("the tag counts the child: %+v", tags)
	}

	for _, query := range []string{
		fmt.Sprintf("?categoryId=%d", cat.ID),
		fmt.Sprintf("?tagId=%d", tag.ID),
	} {
		var got []library.Model
		if err := json.Unmarshal([]byte(mustGet(t, c, "/api/models"+query)), &got); err != nil {
			t.Fatalf("%s: decode: %v", query, err)
		}
		if len(got) != 1 || got[0].Name != "Parent" {
			t.Errorf("%s returned %d rows: %+v", query, len(got), got)
		}
	}
}

// Colours end up in an inline style attribute, so an unvalidated one is a place
// to put arbitrary CSS. The pattern is enforced in the schema and again in the
// service, because the schema alone would be bypassed by any future caller that
// is not this HTTP handler.
func TestCategoryColoursAreRefusedUnlessTheyAreHex(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "painter@example.com")

	for _, tc := range []struct{ what, body string }{
		{"no hash", `{"name":"A","color":"2563eb"}`},
		{"three digits", `{"name":"A","color":"#abc"}`},
		{"a css keyword", `{"name":"A","color":"red"}`},
		{"a css expression", `{"name":"A","color":"#fff;background:url(x)"}`},
		{"empty", `{"name":"A","color":""}`},
	} {
		resp, body := c.send(http.MethodPost, "/api/categories", tc.body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422: %s", tc.what, resp.StatusCode, body)
		}
	}
	if got := decodeCategories(t, mustGet(t, c, "/api/categories")); len(got) != 0 {
		t.Errorf("a refused colour still created %d categories", len(got))
	}
}

// Names are trimmed and length-capped in the service, not only in the schema,
// for the same reason as the colour.
func TestTaxonomyNamesAreRefusedWhenEmpty(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "namer@example.com")

	for _, tc := range []struct{ what, path, body string }{
		{"an empty tag", "/api/tags", `{"name":""}`},
		{"a whitespace tag", "/api/tags", `{"name":"   "}`},
		{"a whitespace material", "/api/materials", `{"name":"\t\n"}`},
		{"a whitespace category", "/api/categories", `{"name":" ","color":"#2563eb"}`},
	} {
		resp, body := c.send(http.MethodPost, tc.path, tc.body)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422: %s", tc.what, resp.StatusCode, body)
		}
	}

	// Trimming is real: the stored name is the trimmed one, which is also what
	// makes " nylon " collide with "Nylon".
	created := decodeLabel(t, mustCreate(t, c, "/api/tags", `{"name":"  spaced  "}`))
	if created.Name != "spaced" {
		t.Errorf("name was stored as %q", created.Name)
	}

	// The cap counts characters, not bytes. Sixty CJK characters are 180 bytes,
	// so a byte-counting cap would refuse a name that is exactly at the limit -
	// and the user has no way to tell why, because the message says characters.
	long := strings.Repeat("\u5bff", maxTaxonomyNameLen)
	resp, body := c.send(http.MethodPost, "/api/tags", `{"name":"`+long+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("a %d character name was refused: got %d: %s", maxTaxonomyNameLen, resp.StatusCode, body)
	}
	tooLong := strings.Repeat("\u5bff", maxTaxonomyNameLen+1)
	resp, body = c.send(http.MethodPost, "/api/tags", `{"name":"`+tooLong+`"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a %d character name was accepted: got %d: %s", maxTaxonomyNameLen+1, resp.StatusCode, body)
	}
}

// The service and the OpenAPI schema each declare this number; the test needs
// it too, and a third copy that drifts is worse than a constant.
const maxTaxonomyNameLen = 60

// A tag can be deleted while a save that names it is in flight, and the two
// steps of that save - reading the tag and writing the join row - do not hold
// the tag still between them. The insert's SELECT takes no lock, and the
// key-share lock the foreign key wants is taken after it, so a delete that
// commits in that gap turns the write into a 23503 rather than into the
// row-count mismatch the same-tag-deleted-earlier case produces.
//
// The interleaving is forced rather than hoped for: the delete is held open in
// its own transaction until the save is demonstrably blocked on it. Without the
// mapping this is a 500 with a Postgres constraint name in it.
func TestSavingAModelWhoseTagIsDeletedMidSave(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "midsave@example.com")

	model := uploadModel(c, "Clip")
	path := fmt.Sprintf("/api/models/%d", model.ID)
	doomed := decodeLabel(t, mustCreate(t, c, "/api/tags", `{"name":"doomed"}`))

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM tags WHERE id = $1`, doomed.ID); err != nil {
		t.Fatalf("delete the tag: %v", err)
	}

	done := make(chan struct{})
	var (
		code int
		body string
	)
	go func() {
		defer close(done)
		var resp *http.Response
		resp, body = c.send(http.MethodPut, path,
			assign("Clip", nil, []int64{doomed.ID}, nil))
		code = resp.StatusCode
	}()

	// Wait for the save to be waiting on the delete's lock. Polling the server's
	// own view of who is blocked is the only way to know the save has already
	// read the tag; a sleep would leave the test asserting whichever ordering
	// the machine felt like that run.
	waitForLockWaiter(t, pool, "model_tags")
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the delete: %v", err)
	}
	<-done

	if code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422: %s", code, body)
	}
	if !strings.Contains(body, "unknown tag") {
		t.Errorf("the refusal does not name what went wrong: %s", body)
	}
	// And nothing half-applied: the whole save rolled back with the tag.
	if got := decodeModel(t, mustGet(t, c, path)); got.Name != "Clip" || len(got.Tags) != 0 {
		t.Errorf("the refused save left something behind: %+v", got)
	}
}

// waitForLockWaiter blocks until some backend is waiting on a lock while running
// a statement mentioning needle.
func waitForLockWaiter(t *testing.T, pool *pgxpool.Pool, needle string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			                 WHERE wait_event_type = 'Lock'
			                   AND query ILIKE '%' || $1 || '%'
			                   AND pid <> pg_backend_pid())`, needle).Scan(&waiting)
		if err != nil {
			t.Fatalf("look for a blocked backend: %v", err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("nothing ever blocked on the tag: the save did not reach the foreign key check")
}
