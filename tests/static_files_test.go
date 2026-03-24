package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticFilesServed(t *testing.T) {
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
		if !strings.Contains(headers.Get("Cache-Control"), "max-age=86400") {
			t.Fatalf("GET %s missing cache header, got %q", path, headers.Get("Cache-Control"))
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
}
