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

// TestNavigationScriptOnlyAnimatesScreenSwaps asserts that the navigation script still scopes its
// transitions to #screen swaps.
//
// The assertions read the TypeScript source rather than the compiled bundle. The source is where
// the behavior is written and where a human edits it; the bundle is a rendering of it, and its
// currency is checked separately, because a bundle that has drifted from its source is a build
// failure rather than a test failure.
func TestNavigationScriptOnlyAnimatesScreenSwaps(t *testing.T) {
	source := readRepoFile(t, filepath.Join("web", "ts", "navigation.ts"))

	if !strings.Contains(source, "target !== screen") {
		t.Fatalf("navigation.ts no longer scopes transitions to #screen swaps: %q", source)
	}
	if !strings.Contains(source, "htmx:beforeRequest") || !strings.Contains(source, "htmx:afterSwap") {
		t.Fatalf("navigation.ts missing expected HTMX hooks: %q", source)
	}
	if !strings.Contains(source, "openMobileNav") || !strings.Contains(source, "closeMobileNav") {
		t.Fatalf("navigation.ts missing mobile navigation helpers: %q", source)
	}
	if !strings.Contains(source, "data-mobile-nav-link") || !strings.Contains(source, "mobileNavFocusTarget") {
		t.Fatalf("navigation.ts missing compact mobile navigation focus handling: %q", source)
	}
}

// TestCompiledNavigationBundleCarriesItsBehaviour asserts that the bundle the browser downloads
// still contains the behavior the source has.
//
// The source assertions above and the currency check in CI are between them enough to prove the
// bundle is right, but this one is the assertion that runs in a test suite, on a machine, with no
// build step: that the file actually served is the file that was compiled.
func TestCompiledNavigationBundleCarriesItsBehaviour(t *testing.T) {
	bundle := readRepoFile(t, filepath.Join("static", "js", "navigation.js"))

	for _, marker := range []string{
		"target !== screen",
		"htmx:beforeRequest",
		"htmx:afterSwap",
		"data-mobile-nav-toggle",
		"data-mobile-nav-link",
	} {
		if !strings.Contains(bundle, marker) {
			t.Errorf("the compiled navigation bundle is missing %q", marker)
		}
	}
}

// readRepoFile reads a file from the repository, from either the repository root or the tests
// directory, since the suite runs with either as its working directory.
func readRepoFile(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		body, err = os.ReadFile(filepath.Join("..", path))
	}
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	return string(body)
}
