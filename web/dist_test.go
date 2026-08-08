package web

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/png"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestDistHasIndex guards the mistake server.New panics on: a missing fs.Sub,
// which leaves index.html buried under build/ instead of at the root.
func TestDistHasIndex(t *testing.T) {
	f, err := Dist.Open("index.html")
	if err != nil {
		t.Fatalf("embedded SPA has no index.html at its root: %v", err)
	}
	_ = f.Close()
}

// TestDistHasAppChunks guards the mistake nothing panics on: dropping `all:`
// from the embed directive. index.html is embedded either way, so the binary
// boots fine and then 404s every script tag it references.
func TestDistHasAppChunks(t *testing.T) {
	matches, err := fs.Glob(Dist, "_app/immutable/entry/*.js")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("embedded SPA has no _app/immutable chunks - did the embed directive lose its `all:` prefix?")
	}
}

// manifestHref pulls the href off the manifest link tag, in either attribute
// order, so the test below can follow it rather than trust it.
var (
	manifestLink = regexp.MustCompile(`<link[^>]*rel="manifest"[^>]*>`)
	hrefAttr     = regexp.MustCompile(`href="([^"]+)"`)
)

// TestIndexHTMLResolvesTheManifest guards a link SvelteKit does not inject: the
// manifest is written by hand in app.html, so an edit that drops it costs the
// app its name, its icons and its installability, and nothing else notices.
// Following the href is what makes this more than a substring check - asserting
// only that `rel="manifest"` appears would pass on a link with no href at all,
// or one pointing at a file the build no longer produces.
func TestIndexHTMLResolvesTheManifest(t *testing.T) {
	tag := manifestLink.FindString(readDist(t, "index.html"))
	if tag == "" {
		t.Fatal("index.html does not link the web manifest")
	}

	href := hrefAttr.FindStringSubmatch(tag)
	if href == nil {
		t.Fatalf("manifest link has no href: %s", tag)
	}

	name := strings.TrimPrefix(href[1], "/")
	if _, err := fs.Stat(Dist, name); err != nil {
		t.Errorf("index.html links manifest %q, which the build does not produce: %v", href[1], err)
	}
}

// TestIndexHTMLHasNoUnsubstitutedPlaceholders catches a silent, self-inflicted
// way to ship an unstyled app: SvelteKit substitutes the *first* occurrence of
// each %sveltekit.*% token, so merely naming one in a comment swallows the
// injected stylesheet link into that comment and leaves the real placeholder
// below as literal text. The page still serves a 200 and still has an index.
func TestIndexHTMLHasNoUnsubstitutedPlaceholders(t *testing.T) {
	if html := readDist(t, "index.html"); strings.Contains(html, "%sveltekit.") {
		t.Error("index.html still contains an unsubstituted sveltekit placeholder - one of them was consumed earlier in the file, probably by a comment that named it")
	}
}

// TestManifestIconsResolve is the one that earns its keep. The foundation's SPA
// handler falls back to index.html for any path it can't open, so a typo'd icon
// src is served as HTML with a 200: every manual check looks fine and the
// browser quietly has no icon. Declared sizes get the same treatment - they're
// checked against the file's real pixels, because a manifest that lies about
// 512x512 is a manifest the browser will reject on its own terms.
func TestManifestIconsResolve(t *testing.T) {
	var manifest struct {
		Name  string `json:"name"`
		Icons []struct {
			Src   string `json:"src"`
			Sizes string `json:"sizes"`
		} `json:"icons"`
	}
	if err := json.Unmarshal([]byte(readDist(t, "manifest.webmanifest")), &manifest); err != nil {
		t.Fatalf("manifest.webmanifest is not valid JSON: %v", err)
	}

	if manifest.Name == "" {
		t.Error("the manifest has no name, so an installed app would be titled after its URL")
	}
	if len(manifest.Icons) == 0 {
		t.Fatal("the manifest declares no icons")
	}

	for _, icon := range manifest.Icons {
		path := strings.TrimPrefix(icon.Src, "/")
		f, err := Dist.Open(path)
		if err != nil {
			t.Errorf("manifest icon %q is not in the embedded SPA: %v", icon.Src, err)
			continue
		}
		cfg, _, err := image.DecodeConfig(f)
		_ = f.Close()
		if err != nil {
			t.Errorf("manifest icon %q does not decode as an image: %v", icon.Src, err)
			continue
		}
		if got := fmt.Sprintf("%dx%d", cfg.Width, cfg.Height); got != icon.Sizes {
			t.Errorf("manifest icon %q declares sizes %q but is really %s", icon.Src, icon.Sizes, got)
		}
	}
}

func readDist(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(Dist, name)
	if err != nil {
		t.Fatalf("embedded SPA has no %s: %v", name, err)
	}
	return string(b)
}
