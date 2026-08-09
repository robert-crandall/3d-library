package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/robert-crandall/3d-library/internal/library"
)

// attach makes one model a version of another, or detaches it when parent is
// nil, and returns the response so a test can assert on a refusal.
func attach(t *testing.T, c *client, id int64, parent *int64) (*http.Response, string) {
	t.Helper()
	body := `{"parentId":null}`
	if parent != nil {
		body = fmt.Sprintf(`{"parentId":%d}`, *parent)
	}
	return c.send(http.MethodPut, fmt.Sprintf("/api/models/%d/parent", id), body)
}

func mustAttach(t *testing.T, c *client, id int64, parent *int64) {
	t.Helper()
	resp, body := attach(t, c, id, parent)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("attach %d: got %d, want 204: %s", id, resp.StatusCode, body)
	}
}

// familyNames is what the version panel would render, in the order it would
// render it.
func familyNames(m library.ModelDetail) []string {
	names := make([]string, 0, len(m.Family))
	for _, member := range m.Family {
		names = append(names, member.Name)
	}
	return names
}

func listTotal(t *testing.T, c *client, query string) int {
	t.Helper()
	var page struct {
		Items []library.Model `json:"items"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal([]byte(mustGet(t, c, "/api/models"+query)), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Items) > page.Total {
		t.Fatalf("more items than total: %d > %d", len(page.Items), page.Total)
	}
	return page.Total
}

// Attaching moves a model out of the library and into its parent's panel, and
// detaching brings it back with everything it owned.
//
// The round trip is the test rather than two separate ones, because a detach
// that lost the model's files would still pass an attach-only test - and losing
// them is the specific thing acceptance criterion 4 promises does not happen.
func TestAttachingAndDetachingAVersion(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "versions@example.com")

	_, body := c.upload("Bracket v1", map[string]string{"v1.stl": "solid one"})
	v1 := decodeModel(t, body)
	_, body = c.upload("Bracket v2", map[string]string{"v2.stl": "solid two", "v2.gcode": "G28"})
	v2 := decodeModel(t, body)

	if got := listTotal(t, c, ""); got != 2 {
		t.Fatalf("before attaching: total = %d, want 2", got)
	}
	// A model on its own still has a family - itself - so the client decides
	// whether to draw a panel from a length rather than from a missing key.
	if got := familyNames(v1); len(got) != 1 || got[0] != "Bracket v1" {
		t.Errorf("a lone model's family = %v, want just itself", got)
	}

	mustAttach(t, c, v2.ID, &v1.ID)

	// It left the grid, and the count went with it.
	if got := listTotal(t, c, ""); got != 1 {
		t.Errorf("after attaching: total = %d, want 1", got)
	}
	parent := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", v1.ID)))
	if got, want := familyNames(parent), []string{"Bracket v1", "Bracket v2"}; !equalStrings(got, want) {
		t.Errorf("parent family = %v, want %v", got, want)
	}
	if parent.Family[1].FileCount != 2 {
		t.Errorf("version file count = %d, want 2", parent.Family[1].FileCount)
	}

	// Opening the version shows its own files and the same panel, so the user
	// can switch back.
	version := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", v2.ID)))
	if version.ParentID == nil || *version.ParentID != v1.ID {
		t.Errorf("version parentId = %v, want %d", version.ParentID, v1.ID)
	}
	if version.FileCount != 2 {
		t.Errorf("version lost its files: fileCount = %d, want 2", version.FileCount)
	}
	if got, want := familyNames(version), []string{"Bracket v1", "Bracket v2"}; !equalStrings(got, want) {
		t.Errorf("family seen from the version = %v, want %v", got, want)
	}

	mustAttach(t, c, v2.ID, nil)

	if got := listTotal(t, c, ""); got != 2 {
		t.Errorf("after detaching: total = %d, want 2", got)
	}
	back := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", v2.ID)))
	if back.ParentID != nil {
		t.Errorf("detached model still has parentId %v", back.ParentID)
	}
	if back.FileCount != 2 {
		t.Errorf("detach lost files: fileCount = %d, want 2", back.FileCount)
	}
	if got := familyNames(back); len(got) != 1 {
		t.Errorf("detached family = %v, want just itself", got)
	}
}

// The family is ordered the way the panel draws it: the root first, then its
// versions newest first.
//
// Ordering by created_at alone would put the root last, since it is the oldest
// thing in the group - which is why the root is ordered separately rather than
// being left to fall wherever its date lands.
func TestFamilyIsRootThenNewestFirst(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "ordering@example.com")

	_, body := c.upload("root", map[string]string{"a.stl": "solid a"})
	root := decodeModel(t, body)
	_, body = c.upload("older", map[string]string{"b.stl": "solid b"})
	older := decodeModel(t, body)
	_, body = c.upload("newer", map[string]string{"c.stl": "solid c"})
	newer := decodeModel(t, body)

	mustAttach(t, c, older.ID, &root.ID)
	mustAttach(t, c, newer.ID, &root.ID)

	got := familyNames(decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", root.ID))))
	if want := []string{"root", "newer", "older"}; !equalStrings(got, want) {
		t.Errorf("family = %v, want %v", got, want)
	}
}

// Every refusal, and each one asserted to have changed nothing.
//
// Asserting the status alone would pass a handler that answered 422 after
// writing, which is the half of acceptance criterion 5 that matters: "refused
// with a readable error and changes nothing".
func TestAttachRefusals(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "refusals@example.com")
	other := signIn(t, ts, "stranger@example.com")

	_, body := c.upload("root", map[string]string{"a.stl": "solid a"})
	root := decodeModel(t, body)
	_, body = c.upload("version", map[string]string{"b.stl": "solid b"})
	version := decodeModel(t, body)
	_, body = c.upload("loner", map[string]string{"c.stl": "solid c"})
	loner := decodeModel(t, body)
	_, body = other.upload("theirs", map[string]string{"d.stl": "solid d"})
	theirs := decodeModel(t, body)

	mustAttach(t, c, version.ID, &root.ID)

	for _, tc := range []struct {
		name   string
		model  int64
		parent *int64
		status int
		says   string
	}{
		{"itself", loner.ID, &loner.ID, http.StatusUnprocessableEntity, "cannot be a version of itself"},
		{"a model that already has versions", root.ID, &loner.ID, http.StatusUnprocessableEntity, "has versions of its own"},
		{"a parent that is itself a version", loner.ID, &version.ID, http.StatusUnprocessableEntity, "already a version of another model"},
		{"another user's model as parent", loner.ID, &theirs.ID, http.StatusNotFound, ""},
		{"another user's model as child", theirs.ID, &loner.ID, http.StatusNotFound, ""},
		{"a parent that does not exist", loner.ID, ptr(int64(0)), http.StatusNotFound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := attach(t, c, tc.model, tc.parent)
			if resp.StatusCode != tc.status {
				t.Fatalf("got %d, want %d: %s", resp.StatusCode, tc.status, out)
			}
			// The sentence is part of the contract: acceptance criterion 5 asks
			// for a readable error, and the frontend renders `detail` verbatim.
			// Asserting the status alone would pass a refusal that told the user
			// nothing about which rule they broke.
			if tc.says == "" {
				return
			}
			var problem struct {
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal([]byte(out), &problem); err != nil {
				t.Fatalf("decode problem: %v (%s)", err, out)
			}
			if !strings.Contains(problem.Detail, tc.says) {
				t.Errorf("detail = %q, want it to mention %q", problem.Detail, tc.says)
			}
		})
	}

	// Nothing moved. The loner is still a root in the grid, the version is
	// still where it was, and the stranger's model is untouched.
	if got := listTotal(t, c, ""); got != 2 {
		t.Errorf("after the refusals: total = %d, want 2 (root and loner)", got)
	}
	if got := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", loner.ID))); got.ParentID != nil {
		t.Errorf("the loner gained a parent: %v", got.ParentID)
	}
	if got := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", root.ID))); got.ParentID != nil {
		t.Errorf("the root gained a parent: %v", got.ParentID)
	}
	if got := decodeModel(t, mustGet(t, other, fmt.Sprintf("/api/models/%d", theirs.ID))); got.ParentID != nil {
		t.Errorf("the stranger's model gained a parent: %v", got.ParentID)
	}
}

// An empty body is not a detach.
//
// parentId is a nullable pointer, so without `required:"true"` huma would parse
// `{}` as null and quietly unmake a version. This is the test that says the
// difference between "absent" and "null" is real.
func TestAttachRequiresParentIDToBePresent(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "requiredfield@example.com")

	_, body := c.upload("root", map[string]string{"a.stl": "solid a"})
	root := decodeModel(t, body)
	_, body = c.upload("version", map[string]string{"b.stl": "solid b"})
	version := decodeModel(t, body)
	mustAttach(t, c, version.ID, &root.ID)

	resp, out := c.send(http.MethodPut, fmt.Sprintf("/api/models/%d/parent", version.ID), `{}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("empty body: got %d, want 422: %s", resp.StatusCode, out)
	}
	if got := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", version.ID))); got.ParentID == nil {
		t.Error("an empty body detached the version")
	}
}

// The one-level rule is the database's, not the handler's.
//
// Every other test here goes through HTTP, so all of them would still pass if
// the rule lived only in Go. This one writes SQL directly, which is the only way
// to tell the difference.
func TestTheDatabaseRefusesAGrandchild(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "constraint@example.com")

	_, body := c.upload("root", map[string]string{"a.stl": "solid a"})
	root := decodeModel(t, body)
	_, body = c.upload("version", map[string]string{"b.stl": "solid b"})
	version := decodeModel(t, body)
	_, body = c.upload("loner", map[string]string{"c.stl": "solid c"})
	loner := decodeModel(t, body)
	mustAttach(t, c, version.ID, &root.ID)

	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`UPDATE models SET parent_id = $1 WHERE id = $2`, version.ID, loner.ID); err == nil {
		t.Error("SQL made a grandchild")
	}
	// A root that has versions cannot become one. The same constraint, from the
	// other side: it is what stops an attach from turning a two-level group into
	// a three-level one.
	if _, err := pool.Exec(ctx,
		`UPDATE models SET parent_id = $1 WHERE id = $2`, loner.ID, root.ID); err == nil {
		t.Error("SQL gave a parent to a model that has versions")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO models (user_id, name, parent_id)
		 SELECT user_id, 'smuggled', $1 FROM models WHERE id = $1`, version.ID); err == nil {
		t.Error("SQL inserted a grandchild")
	}
	if _, err := pool.Exec(ctx,
		`UPDATE models SET parent_id = id WHERE id = $1`, loner.ID); err == nil {
		t.Error("SQL made a model its own parent")
	}
}

// Deleting a parent takes its versions' rows *and* their blobs.
//
// This is the widening milestone 2 asked for and could not test, because nothing
// could make a version yet. The filesystem assertion is the point: models.parent_id
// cascades either way, so a delete that never collected the versions' storage
// keys would pass every row-level check here and still leave their bytes on disk
// forever - a blob with no row, which is the invariant this library exists to
// keep.
func TestDeletingAParentRemovesItsVersionsAndTheirBlobs(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "familykiller@example.com")

	// Real image fixtures, so each model gets a thumbnail sidecar: a delete that
	// removed blobs but forgot sidecars would otherwise leave nothing behind to
	// catch.
	_, body := c.upload("Doomed", map[string]string{"parent.png": thumbFixture(t, "render.png")})
	parent := decodeModel(t, body)
	_, body = c.upload("Doomed v2", map[string]string{
		"child.png": thumbFixture(t, "render.png"),
		"child.stl": "solid c",
	})
	version := decodeModel(t, body)
	_, body = c.upload("Bystander", map[string]string{"z.png": thumbFixture(t, "render.png")})
	bystander := decodeModel(t, body)

	mustAttach(t, c, version.ID, &parent.ID)

	before, sidecarsBefore, _ := blobs(t, dir)
	if len(before) != 4 || len(sidecarsBefore) != 3 {
		t.Fatalf("before delete: %d blobs, %d sidecars; want 4, 3", len(before), len(sidecarsBefore))
	}

	resp, out := c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d", parent.ID), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204: %s", resp.StatusCode, out)
	}

	// The version is gone as a row, not merely detached.
	if resp, _ := c.get(fmt.Sprintf("/api/models/%d", version.ID)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the version survived its parent: got %d", resp.StatusCode)
	}
	for _, f := range version.Files {
		path := fmt.Sprintf("/api/models/%d/files/%d", version.ID, f.ID)
		if resp, _ := c.get(path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s still downloads: got %d", path, resp.StatusCode)
		}
	}

	// The filesystem is the real assertion.
	final, sidecars, temp := blobs(t, dir)
	if len(final) != 1 {
		t.Errorf("blobs = %v, want only the bystander's", final)
	}
	if len(sidecars) != 1 {
		t.Errorf("sidecars = %v, want only the bystander's", sidecars)
	}
	if len(temp) != 0 {
		t.Errorf("temp files left behind: %v", temp)
	}
	if got := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", bystander.ID))); got.FileCount != 1 {
		t.Errorf("the bystander lost files: %+v", got)
	}
	if got := listTotal(t, c, ""); got != 1 {
		t.Errorf("after the delete: total = %d, want 1", got)
	}
}

// Deleting a version leaves its parent whole.
//
// The widened delete collects children before deleting, so a version - which has
// none - has to come out exactly as it did before. A delete that walked to the
// root instead of down from the requested model would take the whole family.
func TestDeletingAVersionLeavesItsParentAlone(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	dir := t.TempDir()
	ts := newTestServer(t, pool, library.Options{Dir: dir})
	c := signIn(t, ts, "versionkiller@example.com")

	_, body := c.upload("Keeper", map[string]string{"keep.stl": "solid keep"})
	parent := decodeModel(t, body)
	_, body = c.upload("Doomed version", map[string]string{"gone.stl": "solid gone"})
	version := decodeModel(t, body)
	mustAttach(t, c, version.ID, &parent.ID)

	resp, out := c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d", version.ID), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204: %s", resp.StatusCode, out)
	}

	kept := decodeModel(t, mustGet(t, c, fmt.Sprintf("/api/models/%d", parent.ID)))
	if kept.FileCount != 1 {
		t.Errorf("the parent lost files: %+v", kept)
	}
	if len(kept.Family) != 1 {
		t.Errorf("family = %v, want just the parent", familyNames(kept))
	}
	if final, _, _ := blobs(t, dir); len(final) != 1 {
		t.Errorf("blobs = %v, want only the parent's", final)
	}
}

// Tags, materials and a category survive the fan-out.
//
// Milestone 7 wrote that the join rows hold no blobs and cascade from models, so
// a delete needs no special case for them. That was true when a delete touched
// one model; this checks it is still true now that one can take a whole family.
func TestDeletingAParentCleansUpTheFamilysLabels(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "labelfamily@example.com")

	tag := createTag(t, c, "shared")
	_, body := c.upload("root", map[string]string{"a.stl": "solid a"})
	root := decodeModel(t, body)
	_, body = c.upload("version", map[string]string{"b.stl": "solid b"})
	version := decodeModel(t, body)
	mustAttach(t, c, version.ID, &root.ID)

	for _, id := range []int64{root.ID, version.ID} {
		resp, out := c.send(http.MethodPut, fmt.Sprintf("/api/models/%d", id),
			fmt.Sprintf(`{"name":"m%d","description":"","printTips":"","sourceUrl":"","categoryId":null,"tagIds":[%d],"materialIds":[]}`, id, tag))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("tag %d: got %d: %s", id, resp.StatusCode, out)
		}
	}

	resp, out := c.send(http.MethodDelete, fmt.Sprintf("/api/models/%d", root.ID), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204: %s", resp.StatusCode, out)
	}

	var joins int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM model_tags WHERE tag_id = $1`, tag).Scan(&joins); err != nil {
		t.Fatalf("count joins: %v", err)
	}
	if joins != 0 {
		t.Errorf("model_tags rows left behind: %d", joins)
	}
	// The tag itself is not a blob and not owned by the model, so it stays.
	if resp, out := c.get("/api/tags"); resp.StatusCode != http.StatusOK {
		t.Errorf("tags: got %d: %s", resp.StatusCode, out)
	}
}

// A brand new model's family is itself.
//
// Create is the one path that builds a ModelDetail by hand instead of re-reading
// through Get, so it is the one place `family` can come back null while the
// schema says it never is. Asserting on the raw JSON rather than the decoded
// struct is deliberate: a nil slice and an empty one decode the same, so a
// struct-level check would pass on exactly the bug this catches.
func TestANewModelsFamilyIsItself(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "newborn@example.com")

	// Deliberately not the `upload` helper. That one returns the body of a
	// follow-up GET, which goes through Get and so through loadFamily - exactly
	// the path that cannot have this bug. The create response is the one that
	// builds a ModelDetail by hand, so it is the one that has to be read.
	ct, part := filePart(t, "a.stl", "solid a")
	resp, body := c.post("Spool Holder", ct, part)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d: %s", resp.StatusCode, body)
	}
	if strings.Contains(body, `"family":null`) {
		t.Fatalf("create returned a null family, which the schema says cannot happen: %s", body)
	}

	m := decodeModel(t, body)
	if got := familyNames(m); len(got) != 1 || got[0] != "Spool Holder" {
		t.Errorf("family = %v, want just the model itself", got)
	}
	if m.Family[0].ID != m.ID {
		t.Errorf("family[0].id = %d, want the model's own %d", m.Family[0].ID, m.ID)
	}
	// The header count and the family's count are two different reads of the
	// same fact, so a Create that hard-codes one of them is worth catching.
	if m.Family[0].FileCount != m.FileCount {
		t.Errorf("family fileCount = %d, model fileCount = %d", m.Family[0].FileCount, m.FileCount)
	}
}

// createTag makes a tag and returns its id, so a test that only needs something
// to attach to a model does not repeat the create-and-decode dance.
func createTag(t *testing.T, c *client, name string) int64 {
	t.Helper()
	resp, body := c.send(http.MethodPost, "/api/tags", fmt.Sprintf(`{"name":%q}`, name))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create tag: got %d: %s", resp.StatusCode, body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode tag: %v", err)
	}
	return created.ID
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
