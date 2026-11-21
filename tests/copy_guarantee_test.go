package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAuthorCopyUsesServerURL pins the v0.5.9 fix: the Copy button is rendered from the Share
// payload's per-row URL, not from an in-memory map that disappears on reload.
func TestAuthorCopyUsesServerURL(t *testing.T) {
	source := readRepoFile(t, filepath.Join("web", "ts", "share.ts"))
	bundle := readRepoFile(t, filepath.Join("static", "js", "share.js"))
	if strings.Contains(source, "mintedLinks") {
		t.Error("the page-session mint cache is still present")
	}
	for _, needle := range []string{
		`copy.setAttribute("data-copy-url", url)`,
		`const url = target.dataset.copyUrl;`,
		`link.url ?? ""`,
	} {
		if !strings.Contains(source, needle) {
			t.Errorf("share.ts does not contain %q", needle)
		}
	}

	// The two explicit actions must stay wired alongside the idempotent primary buttons. These are
	// row-level buttons built in TypeScript, so the compiled bundle must carry them too.
	for _, needle := range []string{
		`/share/link/new`,
		`/share/public-link/rotate`,
		`data-new`,
		`data-public-rotate`,
		`data.created`,
	} {
		if !strings.Contains(source, needle) {
			t.Errorf("share.ts does not contain %q", needle)
		}
		if !strings.Contains(bundle, needle) {
			t.Errorf("the compiled Share bundle does not contain %q", needle)
		}
	}

	// The compiled bundle is what browsers actually execute. A stale bundle would ship the old
	// cache-based behavior even with a correct source file.
	if strings.Contains(bundle, "mintedLinks") {
		t.Error("the compiled Share bundle still carries the page-session cache")
	}
	if !strings.Contains(bundle, "data-copy-url") {
		t.Error("the compiled Share bundle does not copy the server-provided URL")
	}
}

// TestViewerBundleWiresCopy guarantees the reader's three Copy actions are wired by the one script
// the static site ships. The markup is pinned separately in public_navigation_test.go.
func TestViewerBundleWiresCopy(t *testing.T) {
	bundle := readRepoFile(t, filepath.Join("internal", "publish", "assets", "public.js"))
	for _, needle := range []string{
		`data-share-action`,
		`share-address`,
		`copyText`,
	} {
		if !strings.Contains(bundle, needle) {
			t.Errorf("the public bundle does not contain %q", needle)
		}
	}
}

// TestPublisherForbidsSubmissionRoutes ensures no future template can publish a capability URL as a
// link on the reader's site. Today the static output contains none; this pins the validator rule.
func TestPublisherForbidsSubmissionRoutes(t *testing.T) {
	source := readRepoFile(t, filepath.Join("internal", "publish", "validate.go"))
	for _, path := range []string{`"/s/"`, `"/p/"`} {
		if !strings.Contains(source, path) {
			t.Errorf("the publish validator does not forbid %s", path)
		}
	}
}
