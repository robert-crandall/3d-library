package app_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/robert-crandall/go-home-server/server"

	"github.com/robert-crandall/3d-library/web"
)

// How a deploy reaches an installed PWA. There is no code for it, and this test
// is the reason that is safe to say.
//
// Launching the app fetches index.html, which the foundation serves `no-cache`,
// so the client cannot reuse yesterday's copy without asking. Fresh HTML points
// at the new build's scripts, whose filenames contain a content hash, so they
// are fetched by virtue of being different URLs - the year-long `immutable` on
// the old ones is irrelevant because nothing asks for them any more.
//
// That entire argument rests on one header set inside go-home-server's
// setSPACacheControl, which this repo tracks as a version-pinned dependency. If
// a bump ever gave index.html a positive max-age, launching the app would keep
// showing the old build for that long and nothing else here would notice. Hence
// this test, which is deliberately about the contract rather than the exact
// string: `no-cache` or `no-store`, in any order and any case.
// Six digits is a day and a bit. The point is only that the value isn't a token
// number that would have clients re-fetching hashed assets constantly.
var longMaxAge = regexp.MustCompile(`max-age=[0-9]{6,}`)

// SvelteKit's hash is an 8-character token, either the whole stem
// (`B20NDseN.js`) or a suffix on it (`app.D8wVCmbq.js`, `0.DpqJCwbk.css`). This
// is a shape check and not a proof - `bundle12.js` would pass - but it fails
// the names a build that stopped hashing would actually produce.
var hashedName = regexp.MustCompile(`(^|\.)[A-Za-z0-9_-]{8}\.[a-z0-9]+$`)

func TestSPACacheHeaders(t *testing.T) {
	srv := server.New(server.Options{Title: "cache-test", Version: "test", SPA: web.Dist})
	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)

	// Lowercased, because nothing about this contract is case-sensitive and a
	// dependency is free to send `No-Cache`.
	get := func(path string) (*http.Response, string) {
		t.Helper()
		// Don't follow redirects. Reading the header off the *target* of one
		// would quietly answer a different question than the one asked, so a
		// path that starts redirecting fails the status check below instead.
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		t.Cleanup(func() { res.Body.Close() })
		return res, strings.ToLower(res.Header.Get("Cache-Control"))
	}

	// The launch path, plus a deep link, because they are served by different
	// branches: one is a real file, the other is the SPA fallback.
	for _, path := range []string{"/", "/login"} {
		res, cc := get(path)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, res.StatusCode)
		}
		if !strings.Contains(cc, "no-cache") && !strings.Contains(cc, "no-store") {
			t.Errorf("GET %s: Cache-Control %q lets a client serve a stale build without asking; "+
				"launching the app is the only thing that picks up a deploy", path, cc)
		}
	}

	// The other half, and the reason the above is cheap: everything with a
	// content-hashed name is cached hard, so revalidating index.html is one
	// small request and not a re-download of the app.
	//
	// Walking the real embedded build rather than naming a file keeps this
	// honest in both directions, and the names are checked as well as the
	// headers. That pairing is the whole point: `immutable` on a stable name
	// would serve stale JavaScript for a year, so "these are safe to cache
	// forever" is only true because a new build gives them new URLs.
	hashed := 0
	err := fs.WalkDir(web.Dist, "_app/immutable", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		hashed++
		if !hashedName.MatchString(d.Name()) {
			t.Errorf("%s has no content hash in its name, so caching it forever would pin a stale build", path)
		}
		_, cc := get("/" + path)
		// Both halves, because `no-cache, immutable` is a legal header that
		// contains the word this used to look for and would still make every
		// launch re-download the app.
		if !strings.Contains(cc, "immutable") || !longMaxAge.MatchString(cc) {
			t.Errorf("GET /%s: Cache-Control %q, want immutable with a long max-age", path, cc)
		}
		if strings.Contains(cc, "no-cache") || strings.Contains(cc, "no-store") {
			t.Errorf("GET /%s: Cache-Control %q revalidates a content-hashed file", path, cc)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk _app/immutable: %v", err)
	}
	if hashed == 0 {
		t.Fatal("no files under _app/immutable, so this test asserted nothing")
	}
}
