package app_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/robert-crandall/3d-library/internal/library"
)

// Milestone 8: search, sort and pagination.
//
// These go through real HTTP and real Postgres because every one of them is a
// property of the list query - what it matches, what order it returns, and
// which slice of it - and a service test with a fake store would assert the
// slicing of a Go slice instead.
//
// The fixtures are seeded with SQL rather than driven through the upload
// endpoint. Thirty uploads is thirty multipart round trips and thirty blobs on
// disk to test a SELECT, and seeding is also the only way to give two models
// the same created_at, which is the state the sort tiebreak exists for.

// seed inserts root models for the signed-in user, oldest first, one second
// apart unless sameInstant is set.
//
// created_at is set explicitly rather than left to now(), because a test that
// depends on insert order depends on the clock ticking between two inserts, and
// inside one statement it does not.
func seed(t *testing.T, pool *pgxpool.Pool, email string, sameInstant bool, models ...[2]string) {
	t.Helper()
	for i, m := range models {
		offset := fmt.Sprintf("%d seconds", i)
		if sameInstant {
			offset = "0 seconds"
		}
		if _, err := pool.Exec(t.Context(),
			`INSERT INTO models (user_id, name, description, print_tips, created_at)
			 SELECT u.id, $2, $3, $4, timestamptz '2024-01-01 00:00:00Z' + $5::interval
			   FROM users u WHERE u.email = $1`,
			email, m[0], m[1], "", offset); err != nil {
			t.Fatalf("seed %q: %v", m[0], err)
		}
	}
}

// names is the list response's names, in the order the server returned them.
func names(t *testing.T, body string) []string {
	t.Helper()
	page := decodeList(t, body)
	out := make([]string, len(page.Items))
	for i, m := range page.Items {
		out[i] = m.Name
	}
	return out
}

// A page is a real slice of the result set: full, then the remainder, with no
// row appearing twice and none missing.
//
// It seeds more than two pages so an off-by-one in the offset shows up as a
// repeat rather than as a page that happens to look right, and it checks the
// union rather than each page's contents, because two pages that are each
// plausible can still overlap.
func TestPagesDoNotRepeatOrSkip(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "pager@example.com")

	const count = library.PageSize*2 + 6
	fixtures := make([][2]string, count)
	for i := range fixtures {
		fixtures[i] = [2]string{fmt.Sprintf("Model %02d", i), ""}
	}
	seed(t, pool, "pager@example.com", false, fixtures...)

	seen := map[string]int{}
	for _, tc := range []struct {
		page int
		want int
	}{{1, library.PageSize}, {2, library.PageSize}, {3, 6}} {
		body := mustGet(t, c, fmt.Sprintf("/api/models?page=%d", tc.page))
		got := decodeList(t, body)
		if len(got.Items) != tc.want {
			t.Errorf("page %d holds %d models, want %d", tc.page, len(got.Items), tc.want)
		}
		if got.Total != count {
			t.Errorf("page %d says total %d, want %d - the total is of matches, not of the page",
				tc.page, got.Total, count)
		}
		if got.Page != tc.page || got.PageSize != library.PageSize {
			t.Errorf("page %d reported itself as %d (size %d)", tc.page, got.Page, got.PageSize)
		}
		for _, m := range got.Items {
			seen[m.Name]++
		}
	}
	if len(seen) != count {
		t.Errorf("the three pages covered %d distinct models, want %d", len(seen), count)
	}
	for name, n := range seen {
		if n != 1 {
			t.Errorf("%s appeared on %d pages", name, n)
		}
	}
}

// The sort has to be total or the pages disagree with each other. Every model
// here shares one name and one created_at, so a sort with no tiebreak is free
// to return them in any order per query - and then page 2 can repeat a row that
// page 1 already showed.
//
// Without the id tiebreak this fails in practice: Postgres is entitled to
// order the two pages differently, and with a real page size and a full second
// page it does.
func TestSortIsTotal(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "ties@example.com")

	const count = library.PageSize + 6
	fixtures := make([][2]string, count)
	for i := range fixtures {
		fixtures[i] = [2]string{"Same", ""}
	}
	seed(t, pool, "ties@example.com", true, fixtures...)

	for _, tc := range []struct {
		sort       string
		descending bool
	}{
		{"newest", true}, {"oldest", false}, {"name", false}, {"name-desc", true},
	} {
		var ids []int64
		for _, page := range []int{1, 2} {
			for _, m := range decodeList(t, mustGet(t, c,
				fmt.Sprintf("/api/models?sort=%s&page=%d", tc.sort, page))).Items {
				ids = append(ids, m.ID)
			}
		}
		if len(ids) != count {
			t.Fatalf("sort=%s: the two pages held %d of %d models", tc.sort, len(ids), count)
		}
		// The id order is the assertion, not just that the ids are distinct.
		// Distinctness is what a tiebreak buys, but Postgres will often hand
		// back a stable order anyway on a table this small, so a test that only
		// checked for repeats would pass with no tiebreak at all. The contract
		// is that ties break by id in the same direction as the sort, and that
		// is what makes the two pages agree about which row is 25th.
		for i := 1; i < len(ids); i++ {
			if tc.descending && ids[i] >= ids[i-1] {
				t.Errorf("sort=%s: id %d follows %d, want descending", tc.sort, ids[i], ids[i-1])
				break
			}
			if !tc.descending && ids[i] <= ids[i-1] {
				t.Errorf("sort=%s: id %d follows %d, want ascending", tc.sort, ids[i], ids[i-1])
				break
			}
		}
	}
}

// Each ordering, including that name sorts case-insensitively - "apple" after
// "Banana" is what a case-sensitive sort gives you, and it looks broken.
func TestSortOrders(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "sorter@example.com")

	// Lower-case first alphabetically, upper-case second: under a case-sensitive
	// sort "Banana" comes before "apple", which reads as broken.
	seed(t, pool, "sorter@example.com", false,
		[2]string{"apple", ""}, [2]string{"Banana", ""}, [2]string{"cherry", ""})

	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"", []string{"cherry", "Banana", "apple"}},
		{"?sort=newest", []string{"cherry", "Banana", "apple"}},
		{"?sort=oldest", []string{"apple", "Banana", "cherry"}},
		{"?sort=name", []string{"apple", "Banana", "cherry"}},
		{"?sort=name-desc", []string{"cherry", "Banana", "apple"}},
	} {
		got := names(t, mustGet(t, c, "/api/models"+tc.query))
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%q: got %v, want %v", tc.query, got, tc.want)
		}
	}
}

// What search matches, and what it does not.
//
// The wildcard cases are the ones that matter: without escaping, "%" matches
// every model in the library, which is the opposite of a search. Print tips are
// deliberately excluded - they are instructions you read after finding the
// model, and matching them makes "supports" find most of a library.
func TestSearchMatchesNamesAndDescriptions(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "finder@example.com")

	seed(t, pool, "finder@example.com", false,
		[2]string{"Wall bracket", "holds a shelf"},
		[2]string{"Dragon", "a bracket for the tail"},
		[2]string{"Infill test", "50% infill"},
		[2]string{"Snap_fit", "underscore in the name"},
		[2]string{`Back\slash`, "literal backslash"},
		[2]string{"Vase", "smooth"},
	)
	if _, err := pool.Exec(t.Context(),
		"UPDATE models SET print_tips = 'use a brim' WHERE name = 'Vase'"); err != nil {
		t.Fatalf("set print tips: %v", err)
	}

	for _, tc := range []struct {
		what string
		q    string
		want []string
	}{
		{"a word in the name", "wall", []string{"Wall bracket"}},
		{"a word in the description", "shelf", []string{"Wall bracket"}},
		{"a word in either", "bracket", []string{"Dragon", "Wall bracket"}},
		{"case does not matter", "DRAGON", []string{"Dragon"}},
		{"a substring, not a whole word", "rack", []string{"Dragon", "Wall bracket"}},
		// Every token has to match, and each may match either column.
		{"two words, both in the name", "wall bracket", []string{"Wall bracket"}},
		{"two words, one per column", "dragon tail", []string{"Dragon"}},
		{"two words that are never together", "wall dragon", nil},
		// The wildcards. Each of these matches everything if unescaped.
		{"a percent sign", "50%", []string{"Infill test"}},
		{"an underscore", "p_f", []string{"Snap_fit"}},
		// A bare wildcard is the case that decides it: unescaped, each of these
		// matches every model in the library, which is the opposite of a search.
		{"a bare percent", "%", []string{"Infill test"}},
		{"a bare underscore", "_", []string{"Snap_fit"}},
		{"a backslash", `k\s`, []string{`Back\slash`}},
		{"print tips are not searched", "brim", nil},
		{"nothing matches", "zzz", nil},
	} {
		got := names(t, mustGet(t, c, "/api/models?sort=name&q="+url.QueryEscape(tc.q)))
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s (q=%q): got %v, want %v", tc.what, tc.q, got, tc.want)
		}
	}

	// The total is of matches, not of the library, because it is what the count
	// line reports and what tells the user the search narrowed anything.
	if got := decodeList(t, mustGet(t, c, "/api/models?q=bracket")); got.Total != 2 {
		t.Errorf("total = %d for a 2-model search over a 6-model library", got.Total)
	}
	// Whitespace is not a search. The client trims too, but a bookmark can hold
	// ?q=%20 and it must not exclude the whole library.
	if got := decodeList(t, mustGet(t, c, "/api/models?q=%20%20")); got.Total != 6 {
		t.Errorf("a whitespace-only search matched %d models, want the whole library", got.Total)
	}
	if got := decodeList(t, mustGet(t, c, "/api/models")); got.Total != 6 {
		t.Errorf("no search matched %d models, want 6", got.Total)
	}
}

// Search, filter, sort and paging are one query, and the composition is where
// the bugs are: a filter that is dropped once a search is on, a search that
// only applies to the first page, a page that is counted before the filter.
func TestSearchFilterSortAndPageCompose(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "composer@example.com")

	cat := decodeCategory(t, mustCreate(t, c, "/api/categories", `{"name":"Tools","color":"#2563eb"}`))
	tag := decodeLabel(t, mustCreate(t, c, "/api/tags", `{"name":"printed"}`))

	// 30 models that match everything, so the filtered search still spans two
	// pages and page 2 is a real slice rather than the whole result set.
	const matching = library.PageSize + 6
	fixtures := make([][2]string, 0, matching+3)
	for i := range matching {
		fixtures = append(fixtures, [2]string{fmt.Sprintf("Clamp %02d", i), "workshop"})
	}
	// Decoys: right search, wrong category; right category, wrong search.
	fixtures = append(fixtures,
		[2]string{"Clamp loose", "workshop"},
		[2]string{"Vase", "decor"},
		[2]string{"Clamp untagged", "workshop"})
	seed(t, pool, "composer@example.com", false, fixtures...)

	var ids []int64
	rows, err := pool.Query(t.Context(),
		`SELECT id FROM models WHERE name LIKE 'Clamp %' AND name <> 'Clamp loose' ORDER BY id`)
	if err != nil {
		t.Fatalf("read seeded ids: %v", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := pool.Exec(t.Context(),
			"UPDATE models SET category_id = $1 WHERE id = $2", cat.ID, id); err != nil {
			t.Fatalf("categorise: %v", err)
		}
	}
	// The last one keeps the category but not the tag, so a filter that ignored
	// the tag would return 31 and this would catch it.
	for _, id := range ids[:len(ids)-1] {
		if _, err := pool.Exec(t.Context(),
			"INSERT INTO model_tags (model_id, tag_id) VALUES ($1, $2)", id, tag.ID); err != nil {
			t.Fatalf("tag: %v", err)
		}
	}

	base := fmt.Sprintf("/api/models?q=clamp&categoryId=%d&tagId=%d&sort=name", cat.ID, tag.ID)
	first := decodeList(t, mustGet(t, c, base))
	if first.Total != matching {
		t.Fatalf("total = %d, want %d - the count must be of the filtered search, not the library",
			first.Total, matching)
	}
	if len(first.Items) != library.PageSize {
		t.Fatalf("page 1 holds %d, want %d", len(first.Items), library.PageSize)
	}
	second := decodeList(t, mustGet(t, c, base+"&page=2"))
	if len(second.Items) != matching-library.PageSize {
		t.Errorf("page 2 holds %d, want %d", len(second.Items), matching-library.PageSize)
	}
	// Sorted by name across the page boundary, not just within a page: the last
	// of page 1 must precede the first of page 2.
	if last, next := first.Items[len(first.Items)-1].Name, second.Items[0].Name; last >= next {
		t.Errorf("page 1 ends at %q and page 2 starts at %q, which is out of order", last, next)
	}
	for _, m := range append(append([]library.Model{}, first.Items...), second.Items...) {
		if !strings.HasPrefix(m.Name, "Clamp ") || m.Name == "Clamp loose" || m.Name == "Clamp untagged" {
			t.Errorf("%q got through the filter", m.Name)
		}
	}
}

// A page past the end serves the last page and says so, rather than an empty
// grid under a count line claiming 30 models. The response is what the client
// builds Previous and Next from, so it has to report where it actually is.
func TestPagePastTheEndClampsAndSaysSo(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "clamper@example.com")

	seed(t, pool, "clamper@example.com", false,
		[2]string{"One", ""}, [2]string{"Two", ""})

	got := decodeList(t, mustGet(t, c, "/api/models?page=99"))
	if got.Page != 1 || len(got.Items) != 2 || got.Total != 2 {
		t.Errorf("page 99 of a one-page library = %+v, want page 1 with both models", got)
	}

	// And an empty result set still has a page 1, not a page 0.
	empty := decodeList(t, mustGet(t, c, "/api/models?q=nothing&page=4"))
	if empty.Page != 1 || empty.Total != 0 || len(empty.Items) != 0 {
		t.Errorf("an empty search reported %+v, want page 1 of 0 matches", empty)
	}
	if !emptyList(t, mustGet(t, c, "/api/models?q=nothing")) {
		t.Error("an empty search encoded items as null, which the contract says it never is")
	}
}

// Garbage in the query string is a 422 from the schema, not a 500 from the
// database and not a silently different result. The client normalises before it
// asks, so these only arrive from a hand-edited URL - but that is exactly when
// a stack trace in the response would be worst.
func TestBadListParametersAreRefused(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})
	c := signIn(t, ts, "fuzzer@example.com")

	for _, query := range []string{
		"?page=0", "?page=-1", "?page=abc", "?page=1.5",
		"?sort=bogus", "?sort=name;drop", "?q=" + strings.Repeat("x", 101),
	} {
		resp, body := c.get("/api/models" + query)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422: %s", query, resp.StatusCode, body)
		}
	}

	// The length limit counts characters, so a search can be well inside it and
	// still be longer than 100 bytes. Anything that measured the term in bytes
	// and trimmed it would hand Postgres half a character here, and the answer
	// would be a 500 rather than an empty result.
	seed(t, pool, "fuzzer@example.com", false, [2]string{"Euro sign €", "money"})
	long := strings.Repeat("€", 100)
	resp, body := c.get("/api/models?q=" + url.QueryEscape(long))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("100 multi-byte characters: got %d, want 200: %s", resp.StatusCode, body)
	}
	if got := names(t, body); len(got) != 0 {
		t.Errorf("100 multi-byte characters: got %v, want no matches", got)
	}
	// And one that does match, so the test is not passing because the search is
	// broken for anything outside ASCII.
	if resp, body := c.get("/api/models?q=" + url.QueryEscape("€")); resp.StatusCode != http.StatusOK {
		t.Fatalf("one multi-byte character: got %d, want 200: %s", resp.StatusCode, body)
	} else if got := names(t, body); len(got) != 1 {
		t.Errorf("one multi-byte character: got %v, want the euro model", got)
	}
}

// Search and paging are scoped like everything else. A search must not be a way
// around the owner check, and versions must not appear in a search result any
// more than they do in the unsearched grid.
func TestSearchRespectsScopeAndRoots(t *testing.T) {
	dbURL := testDatabase(t)
	pool := testPool(t, dbURL)
	ts := newTestServer(t, pool, library.Options{Dir: t.TempDir()})

	owner := signIn(t, ts, "owner8@example.com")
	seed(t, pool, "owner8@example.com", false,
		[2]string{"Secret bracket", "private"}, [2]string{"Bracket v2", "a version"})

	// Milestone 9 owns the endpoint that nests a model; until then the column
	// is only reachable from SQL, which is also the honest way to set up a
	// state this app's own API cannot yet produce.
	if _, err := pool.Exec(t.Context(),
		`UPDATE models SET parent_id = (SELECT id FROM models WHERE name = 'Secret bracket')
		  WHERE name = 'Bracket v2'`); err != nil {
		t.Fatalf("nest: %v", err)
	}

	got := decodeList(t, mustGet(t, owner, "/api/models?q=bracket"))
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].Name != "Secret bracket" {
		t.Errorf("a search returned a version: %+v", got)
	}

	other := signIn(t, ts, "other8@example.com")
	if !emptyList(t, mustGet(t, other, "/api/models?q=bracket")) {
		t.Error("one user's search reached another user's models")
	}
}
