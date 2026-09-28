package tests

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// This file exists because /logout was registered, implemented and tested, and no control in the
// application reached it.
//
// internal/server/router.go:304 registers priv.Post("/logout", logoutHandler). The handler clears the
// session and redirects to /login, and tests/auth_test.go has posted to it and confirmed the session
// ends for as long as it has existed. What was missing was any form, link, or reference in a single
// template: a working endpoint with no caller, which is the failure mode v0.4.4 wrote
// tests/form_contract_test.go to describe and which then happened again in a place the test does not
// look.
//
// The consequence was narrow and annoying rather than loud. A session lasts eight hours, so the only
// ways to end one were to wait it out or clear cookies by hand. On a shared or borrowed machine the
// writing surface stayed open for the rest of the working day, and nothing in the interface said
// anything was amiss.

// surfacesWithNav are the authenticated surfaces that render the navigation. The sign-out has to be
// reachable from every one of them, because the navigation is the only chrome they share -- there is
// no footer and no account page.
var surfacesWithNav = []string{
	"/dashboard",
	"/editor",
	"/library",
	"/caelum",
	"/share",
	"/recycle",
}

// TestEverySurfaceOffersASignOut asserts the control exists, on every surface, and is complete.
//
// The completeness part is the point, and it is the same lesson as the editor's create form: a form
// that exists but omits its synchroniser token is refused with 403, and a form posting to /logout that
// is refused leaves the author exactly where they were, believing they had signed out. Asserting that
// the form merely exists would have passed for a form that cannot work.
func TestEverySurfaceOffersASignOut(t *testing.T) {
	connectTestDB(t)
	srv := newTestServer(t)

	for _, surface := range surfacesWithNav {
		status, page, _ := get(t, srv.URL+surface, nil)
		if status != http.StatusOK {
			t.Errorf("GET %s returned %d, want 200; the surface cannot be judged for a sign-out it "+
				"may never have rendered", surface, status)
			continue
		}

		var signingOut []renderedForm
		for _, form := range parseForms(t, page) {
			if form.action == "/logout" {
				signingOut = append(signingOut, form)
			}
		}
		if len(signingOut) == 0 {
			t.Errorf("%s renders no control that signs out; the only way to end a session is to wait "+
				"out its eight-hour lifetime", surface)
			continue
		}
		for _, form := range signingOut {
			if _, ok := form.fields["csrf"]; !ok {
				t.Errorf("%s renders a sign-out with no csrf field, so the router refuses it with 403 "+
					"and the author stays signed in believing otherwise. It carries: %v",
					surface, form.fields)
			}
		}

		// Not an htmx request. A sign-out that depends on script fails on exactly the surface where
		// the script did not load, and this release has spent its length removing a dependence on
		// script that was never running.
		if strings.Contains(page, `hx-post="/logout"`) {
			t.Errorf("%s signs out through htmx; a form that needs JavaScript to submit is a sign-out "+
				"that fails whenever the script does", surface)
		}
	}
}

// TestTheSignOutFormEndsTheSession drives the control the way a browser would.
//
// It submits the rendered form's own fields rather than calling postForm, for the reason the editor
// test explains: postForm attaches the token itself, so a form missing the token would pass. Here the
// form is parsed from the page and submitted as found, so this fails if the control is incomplete.
func TestTheSignOutFormEndsTheSession(t *testing.T) {
	connectTestDB(t)
	srv := newTestServer(t)

	_, page, _ := get(t, srv.URL+"/library", nil)

	action, fields := parseFormByAction(t, page, "/logout")
	if action == "" {
		t.Fatal("the library surface rendered no form posting to /logout")
	}
	if _, ok := fields["csrf"]; !ok {
		t.Fatalf("the sign-out form carries no csrf field, so it would be refused with 403; it "+
			"carries: %v", fields)
	}

	// The redirect must be observed rather than followed, because following it is the browser's
	// behavior and the assertion here is that the session ended, not where we landed.
	authClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { authClient.CheckRedirect = nil }()

	resp := doPostForm(t, authClient, srv.URL+action, fields)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("submitting the sign-out form returned %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("the sign-out redirected to %q, want /login", loc)
	}

	// And the session really is gone, which is the whole point of the control.
	after := doGet(t, authClient, srv.URL+"/library")
	defer after.Body.Close()
	if after.StatusCode != http.StatusSeeOther {
		t.Fatalf("after signing out, GET /library returned %d, want 303 to /login; the control did "+
			"not end the session", after.StatusCode)
	}
}

// parseFormByAction returns the first form in the page with the given plain action attribute, together
// with the fields it carries.
func parseFormByAction(t *testing.T, page, want string) (string, url.Values) {
	t.Helper()

	for _, form := range parseForms(t, page) {
		if form.action == want {
			return form.action, form.fields
		}
	}
	return "", nil
}
