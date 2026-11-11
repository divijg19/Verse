package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStaticFilesAreRevalidated asserts that a static asset is rechecked with the server on every
// request rather than served from a long-lived cache.
//
// The author side serves its assets from fixed, unhashed URLs, so a cache long enough to outlive a
// deploy keeps a browser on the previous release's script and stylesheet. That is not a
// hypothetical: the Focus Mode close control stayed broken for a day after its fix was deployed,
// because the browser was still running the older script.
//
// Revalidation is what makes a shipped fix visible. http.ServeFile sets Last-Modified and answers a
// matching If-Modified-Since with 304, so the request costs one round trip and no body.
func TestStaticFilesAreRevalidated(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	paths := []string{"/static/js/navigation.js"}
	if _, err := os.Stat("static/css/output.css"); err == nil {
		paths = append(paths, "/static/css/output.css")
	}
	for _, path := range paths {
		status, _, headers := get(t, srv.URL+path, nil)
		if status != 200 {
			t.Fatalf("GET %s status = %d, want 200", path, status)
		}

		cacheControl := headers.Get("Cache-Control")
		if !strings.Contains(cacheControl, "no-cache") {
			t.Fatalf("GET %s Cache-Control = %q, want no-cache so a deployed fix reaches the browser", path, cacheControl)
		}
		if strings.Contains(cacheControl, "max-age") {
			t.Fatalf("GET %s Cache-Control = %q carries a max-age; an unhashed asset URL must be "+
				"revalidated, not held", path, cacheControl)
		}
		if headers.Get("Last-Modified") == "" {
			t.Fatalf("GET %s has no Last-Modified, so revalidation has nothing to compare against", path)
		}
	}
}

func TestNavigationScriptOnlyAnimatesScreenSwaps(t *testing.T) {
	path := filepath.Join("static", "js", "navigation.js")
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join("..", path)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read navigation.js failed: %v", err)
	}

	script := string(body)
	if !strings.Contains(script, "target !== screen") {
		t.Fatalf("navigation.js no longer scopes transitions to #screen swaps: %q", script)
	}
	if !strings.Contains(script, "htmx:beforeRequest") || !strings.Contains(script, "htmx:afterSwap") {
		t.Fatalf("navigation.js missing expected HTMX hooks: %q", script)
	}
	if !strings.Contains(script, "verseOpenMobileNav") || !strings.Contains(script, "verseCloseMobileNav") {
		t.Fatalf("navigation.js missing mobile navigation helpers: %q", script)
	}
	if !strings.Contains(script, "data-mobile-nav-link") || !strings.Contains(script, "verseMobileNavFocusTarget") {
		t.Fatalf("navigation.js missing compact mobile navigation focus handling: %q", script)
	}
}
