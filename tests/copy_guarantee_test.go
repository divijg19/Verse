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
		`rowControls`,
		`isEnded`,
		`unrecoverable`,
		`verse-share-link-row-ended`,
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
		`dataset.shareAddress`,
		`hidden = false`,
		`copyText`,
	} {
		if !strings.Contains(bundle, needle) {
			t.Errorf("the public bundle does not contain %q", needle)
		}
	}
}

// TestShareBundleWiresTheSuggestionsOverlay pins the v0.5.15 author-side surface in both the
// source and the compiled bundle: a stale bundle would ship rows with a dead opener.
func TestShareBundleWiresTheSuggestionsOverlay(t *testing.T) {
	source := readRepoFile(t, filepath.Join("web", "ts", "share.ts"))
	bundle := readRepoFile(t, filepath.Join("static", "js", "share.js"))
	for _, needle := range []string{
		`data-suggestions-overlay`,
		`data-suggestions-list`,
		`showModal`,
		`lockBodyScroll`,
		`unlockBodyScroll`,
		`suggestionsLocked`,
		`suggestionsOpen`,
		`text.textContent = submission.content`,
		`publicSubmissions`,
		`dialog.addEventListener("close"`,
		`dialog.close()`,
		`opener.focus()`,
		`opener?.isConnected`,
		`dataset.suggestionsWired`,
		`requestAnimationFrame`,
	} {
		if !strings.Contains(source, needle) {
			t.Errorf("share.ts does not contain %q", needle)
		}
		if !strings.Contains(bundle, needle) {
			t.Errorf("the compiled Share bundle does not contain %q", needle)
		}
	}
	for _, needle := range []string{`innerHTML`, `insertAdjacentHTML`, `outerHTML`} {
		if strings.Contains(source, needle) || strings.Contains(bundle, needle) {
			t.Errorf("the suggestions overlay parses HTML; guest text must use textContent: %q", needle)
		}
	}
}

// TestPublicSubmitBundleWiresCopy pins the v0.5.14 Copy Text path. The compiled bundle is what the
// browser executes, so the needles must be there as well as in the source: a stale bundle would ship
// a page with a hidden, dead button.
func TestPublicSubmitBundleWiresCopy(t *testing.T) {
	source := readRepoFile(t, filepath.Join("web", "ts", "public_submit.ts"))
	bundle := readRepoFile(t, filepath.Join("static", "js", "public_submit.js"))
	for _, needle := range []string{
		`copyText`,
		`textContent`,
		`data-copy-title`,
		`data-copy-body`,
		`data-public-copy-text`,
		`data-public-actions`,
		`data-public-copy-hint`,
		`hidden = false`,
		`pageshow`,
	} {
		if !strings.Contains(source, needle) {
			t.Errorf("public_submit.ts does not contain %q", needle)
		}
		if !strings.Contains(bundle, needle) {
			t.Errorf("the compiled public submission bundle does not contain %q", needle)
		}
	}
	// The capability URL is a write capability. It must never be an attribute or a copy target on
	// this anonymous page.
	for _, needle := range []string{`/p/`, `data-share-address`, `data-copy-url`, `innerHTML`} {
		if strings.Contains(source, needle) {
			t.Errorf("public_submit.ts must not carry the capability URL: %q", needle)
		}
		if strings.Contains(bundle, needle) {
			t.Errorf("the compiled public submission bundle must not carry the capability URL: %q", needle)
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

// TestGuestAutosaveCarriesTheBranch pins the browser-path fix: a FormData built from nothing drops
// the form's hidden fields, so the branch has to be copied explicitly into the autosave body.
func TestGuestAutosaveCarriesTheBranch(t *testing.T) {
	for _, file := range []string{
		filepath.Join("web", "ts", "guest.ts"),
		filepath.Join("static", "js", "guest.js"),
	} {
		body := readRepoFile(t, file)
		if !strings.Contains(body, `"branch"`) || !strings.Contains(body, `[data-guest-branch]`) {
			t.Errorf("%s does not include the selected branch in autosave", file)
		}
	}
}

// TestEditorFocusRestoreIsInsideTheBundle pins the CSP-safe replacement for hx-on: the markup must
// no longer rely on inline handlers, and the first-party editor bundle must wire htmx:afterRequest.
func TestEditorFocusRestoreIsInsideTheBundle(t *testing.T) {
	markup := readRepoFile(t, filepath.Join("templ", "editor.templ"))
	if strings.Contains(markup, "hx-on") {
		t.Error("editor still uses hx-on, which CSP blocks")
	}
	bundle := readRepoFile(t, filepath.Join("static", "js", "editor.js"))
	for _, needle := range []string{"htmx:afterRequest", "after-request"} {
		if strings.Contains(bundle, needle) {
			return
		}
	}
	t.Error("editor bundle does not wire htmx:afterRequest for focus restore")
}
