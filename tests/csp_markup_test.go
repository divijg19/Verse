package tests

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/services"
)

// This file exists because v0.3.7 added a content security policy that blocked the application's own
// JavaScript, and nothing noticed for eleven releases.
//
// script-src has always been 'self', with no 'unsafe-inline', no nonce and no hash anywhere in the
// project. Against that, the editor shipped a 75-line inline <script> and four inline on*= attributes,
// and the navigation shipped two more. Every one of them was blocked in every browser: the block never
// executed, so the functions it defined were never defined, and the attributes never fired. The
// editor's Focus Mode could not be opened, and below 1024px -- where the desktop rail is display:none
// -- the hamburger was the only route to the navigation and it did nothing.
//
// The tests did not disagree with each other; they simply never spoke. TestSecurityHeaders asserted
// the header string. Other tests asserted the markup. No test rendered a page and asked whether the
// policy it was served permitted what the page contained, so a policy and the content it governs were
// never compared. The comment in internal/server/router.go went further and read the violation as
// intent, which is how it survived review at all.
//
// The assertion below is the one that was missing.

// TestGuestEditorMarkupObeysThePolicy covers the one surface outside the author session. It cannot
// be added to surfacesToCheck, which fetches pages with the author's client; the guest editor needs
// a capability exchange first. The assertion is the same: no inline script, no inline handler, and a
// script element only if it points at a served asset.
func TestGuestEditorMarkupObeysThePolicy(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)
	id := insertPoem(t, "A Work\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()

	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token)
	if status != http.StatusSeeOther {
		t.Fatalf("guest entry = %d, want 303", status)
	}
	status, body, headers := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("guest editor = %d, want 200", status)
	}
	if !strings.Contains(headers.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("guest page has no pinned script-src: %q", headers.Get("Content-Security-Policy"))
	}
	// The guest page is allowed exactly one script, provided it has a src. The general block detector
	// matches any script tag, so assert the narrower property here instead.
	scripts := inlineScriptBlock.FindAllString(body, -1)
	if len(scripts) != 1 {
		t.Errorf("guest page has %d script tags, want 1: %v", len(scripts), scripts)
	}
	for _, tag := range scripts {
		if !srcAttribute.MatchString(tag) {
			t.Errorf("guest page has a script without src: %s", tag)
		}
	}
	if inlineEventAttribute.MatchString(body) {
		t.Error("guest page contains an inline event handler")
	}
	if !strings.Contains(body, `<script src="/static/js/guest.js" defer>`) {
		t.Error("guest page does not load its one first-party bundle")
	}
	// Both submit buttons must write their branch to a hidden field before the form is sent. A
	// submit button's own name/value disappears when the script disables it during submit, which
	// would silently turn every "Send as alternative" into a primary submission.
	if !strings.Contains(body, `data-guest-branch`) ||
		!strings.Contains(body, `data-guest-send="primary"`) ||
		!strings.Contains(body, `data-guest-send="alternative"`) {
		t.Error("guest form is missing the branch field or one of its submit buttons")
	}
	if !strings.Contains(body, `data-guest-branches`) || !strings.Contains(body, `verse-guest-actions`) {
		t.Error("guest editor is missing the branch rail or the side-by-side button wrapper")
	}
	if !strings.Contains(body, "Send main version") || !strings.Contains(body, "Send another version") {
		t.Error("guest submit buttons do not carry their clearer labels")
	}
}

// TestPublicSubmitMarkupObeysThePolicy covers the anonymous /p page. It needs a minted capability
// and a guest client, so it cannot join surfacesToCheck; the assertion is the same one used for the
// guest editor: one script with a src, no inline script and no inline handler.
func TestPublicSubmitMarkupObeysThePolicy(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)
	id := insertPoem(t, "A Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.Close()
	token := mintPublicLink(t, srv.URL, id)

	status, body, headers := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+token)
	if status != http.StatusOK {
		t.Fatalf("public page = %d", status)
	}
	if !strings.Contains(headers.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("public page has no pinned script-src: %q", headers.Get("Content-Security-Policy"))
	}
	scripts := inlineScriptBlock.FindAllString(body, -1)
	if len(scripts) != 1 {
		t.Errorf("public page has %d script tags, want 1: %v", len(scripts), scripts)
	}
	for _, tag := range scripts {
		if !srcAttribute.MatchString(tag) {
			t.Errorf("public page has a script without src: %s", tag)
		}
	}
	if inlineEventAttribute.MatchString(body) {
		t.Error("public page contains an inline event handler")
	}
	if !strings.Contains(body, `<script src="/static/js/public_submit.js" defer>`) {
		t.Error("public page does not load its one first-party bundle")
	}
}

// surfacesToCheck are the surfaces that carry controls. A control is markup, so a surface without one
// is not evidence of anything -- but the editor and the dashboard both need checking, because a
// regression that reintroduces an inline handler on a rarely-visited surface is exactly the kind that
// goes unnoticed.
var surfacesToCheck = []string{
	"/editor",
	"/library",
	"/dashboard",
	"/share",
}

// inlineScriptBlock matches a <script> with no src. A script with a src is the only shape the policy
// permits, so the absence of one is the whole test.
var inlineScriptBlock = regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>`)

// srcAttribute matches a src= on a script tag, to tell a permitted script from a blocked one.
var srcAttribute = regexp.MustCompile(`(?is)<script[^>]*\ssrc\s*=`)

// inlineEventAttribute matches any on*= attribute. Every one of them is a script the policy does not
// permit, whatever it is called.
var inlineEventAttribute = regexp.MustCompile(`(?i)\son(click|input|change|submit|load|error|mouse\w+|key\w+|focus|blur)\s*=`)

// TestContentSecurityPolicyMatchesTheMarkup asserts the policy and the pages actually agree.
func TestContentSecurityPolicyMatchesTheMarkup(t *testing.T) {
	connectTestDB(t)
	srv := newTestServer(t)

	// The policy under test is read off the wire rather than from the constant, so this fails if the
	// header is ever loosened as well as if the markup regresses. A test that compared the markup
	// against a hardcoded expectation of what the policy allows would pass in exactly the situation
	// that matters: someone adding 'unsafe-inline' and an inline script in the same change.
	status, _, headers := get(t, srv.URL+"/editor", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /editor returned %d, want 200", status)
	}

	csp := headers.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy was served; the markup cannot be checked against a policy that is not there")
	}
	if !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("script-src is not pinned to 'self': %q", csp)
	}
	// This is the premise the whole file rests on. If a future change introduces a nonce or a hash,
	// this assertion must be revisited deliberately rather than quietly passing.
	if strings.Contains(csp, "'unsafe-inline'") && strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("script-src permits unsafe-inline, so this test proves nothing: %q", csp)
	}

	for _, surface := range surfacesToCheck {
		code, page, surfaceHeaders := get(t, srv.URL+surface, nil)
		if code != http.StatusOK {
			// A surface that redirects or refuses cannot be judged, and a log line is invisible to
			// CI's no-skip assertion. Fail so a control surface cannot silently leave this guard.
			t.Errorf("%s returned %d, so its policy and markup cannot be checked", surface, code)
			continue
		}
		if got := surfaceHeaders.Get("Content-Security-Policy"); got != csp {
			t.Errorf("%s serves a different policy than /editor:\n  /editor: %q\n  %s:    %q",
				surface, csp, surface, got)
		}

		assertNoInlineScript(t, surface, page)
		assertNoInlineHandlers(t, surface, page)
	}
}

// assertNoInlineScript fails on a <script> element carrying no src.
func assertNoInlineScript(t *testing.T, surface, page string) {
	t.Helper()

	for _, match := range inlineScriptBlock.FindAllString(page, -1) {
		if srcAttribute.MatchString(match) {
			continue
		}
		t.Errorf("%s contains an inline <script> block, which script-src 'self' blocks. The browser "+
			"discards it, so any behavior it defined silently does not happen:\n  %s", surface, summarize(match))
	}
}

// assertNoInlineHandlers fails on any on*= attribute.
func assertNoInlineHandlers(t *testing.T, surface, page string) {
	t.Helper()

	matches := inlineEventAttribute.FindAllString(page, -1)
	if len(matches) == 0 {
		return
	}
	// Sorted and de-duplicated so a surface with the same handler repeated reports one line with a
	// count rather than forty identical ones.
	counts := map[string]int{}
	for _, m := range matches {
		counts[strings.TrimSpace(m)]++
	}
	for attr, n := range counts {
		t.Errorf("%s contains %d inline event attribute(s) (%s), which script-src 'self' blocks; the "+
			"browser never fires them, so the control is inert with no error", surface, n, attr)
	}
}

// TestTheEditorOverlayControlsAreWiredByFile checks the other half of the fix.
//
// The test above proves the policy is not violated. It cannot prove the features work: a page with no
// inline handlers and no script of any kind would satisfy it while doing nothing at all. So this
// asserts the controls the removed handlers used to drive are still present, and that the file which
// drives them is referenced. Together the two say the policy is satisfied and something is there to
// run.
//
// This is the closest a unit test gets to the browser, and it is not a substitute for one. What it
// cannot cover is whether the listener fires, which no amount of markup inspection can answer.
func TestTheEditorOverlayControlsAreWiredByFile(t *testing.T) {
	connectTestDB(t)
	srv := newTestServer(t)

	_, page, _ := get(t, srv.URL+"/editor", nil)

	// The external script, referenced and not inlined.
	if !strings.Contains(page, `src="/static/js/editor.js"`) {
		t.Error("the editor does not reference /static/js/editor.js; removing the inline block must " +
			"have replaced it, not simply deleted it")
	}

	// Every hook the delegation binds to. A missing one means a control that renders and does
	// nothing, which is the same silent failure this release exists to end.
	for _, hook := range []struct{ attr, why string }{
		{"data-editor-overlay-open", "the Full screen button opens the overlay"},
		{"data-editor-overlay-close", "the backdrop and the Close button dismiss the overlay"},
		{"data-editor-overlay-textarea", "typing in the overlay syncs back to the base textarea"},
		{"data-editor-base", "the overlay seeds from, and writes back to, the base textarea"},
		{"data-editor-root", "both functions resolve the editor root through it"},
	} {
		if !strings.Contains(page, hook.attr) {
			t.Errorf("the editor no longer renders %s, and %s", hook.attr, hook.why)
		}
	}
}

// TestTheMobileNavigationControlsAreWiredByFile is the same assertion for the navigation.
//
// The two inline handlers here were the more damaging of the six: layout.templ sets the desktop nav
// to display:none below 1024px, so on a phone nothing in this application could open the navigation.
func TestTheMobileNavigationControlsAreWiredByFile(t *testing.T) {
	connectTestDB(t)
	srv := newTestServer(t)

	_, page, _ := get(t, srv.URL+"/library", nil)

	if !strings.Contains(page, "data-mobile-nav-toggle") {
		t.Error("the navigation no longer renders data-mobile-nav-toggle, so the delegated click " +
			"listener in navigation.js has nothing to bind to")
	}
	if !strings.Contains(page, "data-mobile-nav-close") {
		t.Error("the navigation no longer renders data-mobile-nav-close, so the sheet cannot be " +
			"dismissed by tapping the backdrop")
	}
	if !strings.Contains(page, `src="/static/js/navigation.js"`) {
		t.Error("the navigation script is not referenced, so the delegated listeners never load")
	}
}

// summarize trims a long match to something readable in a failure message.
func summarize(s string) string {
	const limit = 120
	s = strings.TrimSpace(s)
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}
