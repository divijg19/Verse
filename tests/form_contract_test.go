package tests

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/services"
)

// This file exists because v0.4.4 shipped a recycle whose two recovery affordances were both wired to
// nothing that works, and no test noticed.
//
// The recycle's "Restore" button posted to /poem/restore -- the version-restore route -- carrying only
// an id and no version field, so the handler refused it with 400. htmx does not swap a non-2xx response
// by default, so the button was inert: no error, no confirmation. Meanwhile /poem/undelete, the route
// that actually undeletes, had no caller in any template.
//
// The reason it shipped is the reason this file exists. The suite tested the endpoints directly:
// TestRecycleRoutesExerciseTheFeature posted to /poem/undelete by hand and passed, because the route
// worked. Nothing ever read the rendered HTML and asked whether the button pointed at it. A handler
// test and a working button are different claims, and only one of them was being made.
//
// The check is deliberately indirect. It does not compare targets against a hand-maintained list of
// routes, because that list is a second source of truth which drifts. It probes each target and
// asserts the response is neither 404 nor 405, which is true if and only if the route exists.
//
// Probing with POST sends a deliberately invalid synchroniser token, so requireCSRF refuses with 403
// before any handler work. That is what makes the probe non-mutating: it cannot save, delete or
// restore anything, and it does not touch the login rate limiter, which is consulted after CSRF.

// hxAttribute matches an htmx method attribute and its target.
var hxAttribute = regexp.MustCompile(`hx-(post|get|delete|put|patch)="([^"]+)"`)

// formAction matches a plain form action. A form with no method attribute submits by GET, per the
// HTML specification, and that is how this one is asserted.
var formAction = regexp.MustCompile(`<form[^>]*\saction="([^"]+)"`)

// formMethod matches an explicit non-GET method on a form, so a future method="post" is not probed as
// a GET and reported as a spurious 405.
var formMethod = regexp.MustCompile(`<form[^>]*\smethod="([^"]+)"`)

// formTag matches a whole form element, so the method and action can be read from the same tag.
var formTag = regexp.MustCompile(`(?s)<form[^>]*>`)

// internalHref matches a link to a path on this service.
//
// Included because a dead link is the same defect as a dead form target: v0.4.4 shipped a recycle
// whose button pointed at the wrong route, and a link that 404s is invisible in exactly the same way.
// External URLs and fragments are excluded by the extractor rather than here.
var internalHref = regexp.MustCompile(`href="(/[^"]*)"`)

// TestEveryFormTargetIsARealRoute is the check that would have caught both v0.4.4 defects.
func TestEveryFormTargetIsARealRoute(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	// Seed the data the dynamic targets need: a live poem with a retained revision, and a deleted
	// one. Without these, the /editor/{id} and /poem/{id}/history targets are not rendered at all
	// and the surfaces that carry them would be silently untested.
	live := insertPoem(t, "a live work")
	if err := services.UpdatePoem(context.Background(), live, "a live work, edited"); err != nil {
		t.Fatalf("edit the live poem so it has history: %v", err)
	}
	deleted := insertPoem(t, "a deleted work")
	if err := services.SoftDeletePoem(context.Background(), deleted); err != nil {
		t.Fatalf("delete a poem so the recycle is not empty: %v", err)
	}

	srv := newTestServer(t)

	surfaces := []string{
		"/login",
		"/",
		"/dashboard",
		"/editor",
		"/editor/" + live,
		"/library",
		"/caelum",
		"/share",
		"/poem/" + live,
		"/poem/" + live + "/history",
		"/poem/" + deleted + "/history",
		"/recycle",
		"/prompt",
	}

	// Collected rather than asserted per surface, so the same target reached from two screens is
	// probed once and a duplicate is not mistaken for coverage.
	probed := map[string]bool{}
	failures := 0

	for _, surface := range surfaces {
		status, body, _ := get(t, srv.URL+surface, nil)
		if status != http.StatusOK {
			t.Errorf("GET %s = %d, want 200; cannot check its forms", surface, status)
			continue
		}

		targets := extractFormTargets(t, body)

		if len(targets) == 0 {
			t.Logf("GET %s rendered no forms or htmx targets", surface)
			continue
		}
		t.Logf("GET %s -> %d target(s)", surface, len(targets))

		for _, target := range targets {
			if probed[target.key()] {
				continue
			}
			probed[target.key()] = true

			got := probeTarget(t, srv.URL+target.path, target.method)
			if got == http.StatusNotFound || got == http.StatusMethodNotAllowed {
				failures++
				t.Errorf("%s on %s points at %s %s, which returned %d.\n"+
					"  The control exists but nothing reaches it, or the method is wrong.\n"+
					"  htmx does not swap a non-2xx response, so this renders as a button that "+
					"silently does nothing.",
					target.method, surface, target.method, target.path, got)
			}
		}
	}

	if failures > 0 {
		t.Errorf("%d form target(s) in the rendered UI do not reach a route", failures)
	}
	if len(probed) == 0 {
		t.Fatal("no targets were collected; the extraction is broken and this test proves nothing")
	}
	t.Logf("probed %d distinct target(s)", len(probed))
}

// TestTheRecycleRestoreButtonUndeletes pins the specific bug this release fixes.
//
// Separate from the general sweep above, and narrower on purpose: a test that only says "every target
// resolves" will not notice that the recycle's Restore button resolves to the *wrong* control, because
// /poem/restore is a real route. It answers 403 for want of a version field, which is a success as far
// as a reachability check is concerned and a failure as far as the author is concerned.
func TestTheRecycleRestoreButtonUndeletes(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "deleted by accident")
	if err := services.SoftDeletePoem(context.Background(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	srv := newTestServer(t)

	_, body, _ := get(t, srv.URL+"/recycle", nil)

	// The form that carries the Restore button must post the undelete route.
	undelete := formPostingTo(t, body, "/poem/undelete")
	if undelete == "" {
		t.Fatal("the recycle rendered no form posting to /poem/undelete; " +
			"the Restore button cannot undelete anything")
	}
	if !strings.Contains(undelete, `name="id"`) {
		t.Errorf("the undelete form carries no id field: %s", undelete)
	}
	// It must not post to the version-restore route, which is a different operation that
	// requires a version field this form has no way to supply.
	if strings.Contains(undelete, "/poem/restore") {
		t.Error("the recycle's undelete form posts to /poem/restore, the version-restore route; " +
			"that handler requires a version field and refuses with 400")
	}
}

// formElement matches a whole form element, opening tag through closing tag.
//
// The whole element rather than the opening tag alone, because the fields a control submits live in
// the body. Asserting that an undelete form carries an id means looking inside it.
var formElement = regexp.MustCompile(`(?s)<form[^>]*>.*?</form>`)

// formPostingTo returns the first form element whose hx-post is the given path.
func formPostingTo(t *testing.T, body, path string) string {
	t.Helper()
	for _, form := range formElement.FindAllString(body, -1) {
		if strings.Contains(form, `hx-post="`+path+`"`) {
			return form
		}
	}
	return ""
}

// formTarget is one reachable control found in rendered HTML.
type formTarget struct {
	method string
	path   string
}

func (f formTarget) key() string { return f.method + " " + f.path }

// extractFormTargets pulls every htmx target and form action out of a rendered page.
//
// Query strings are dropped: the route is what is being checked, and /poems?q=x and /poems must not
// count as two controls. A path that is empty after trimming is an in-page anchor or a template
// expression that produced nothing, and is skipped rather than probed as "/".
func extractFormTargets(t *testing.T, body string) []formTarget {
	t.Helper()

	var out []formTarget
	seen := map[string]bool{}
	add := func(method, raw string) {
		path := strings.TrimSpace(raw)
		if path == "" || strings.HasPrefix(path, "#") {
			return
		}
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
		if !strings.HasPrefix(path, "/") {
			return // an absolute URL to somewhere else; not ours to resolve
		}
		target := formTarget{method: strings.ToUpper(method), path: path}
		if seen[target.key()] {
			return
		}
		seen[target.key()] = true
		out = append(out, target)
	}

	for _, m := range hxAttribute.FindAllStringSubmatch(body, -1) {
		add(m[1], m[2])
	}

	// A form's method defaults to GET, so the method attribute is read from the same tag rather than
	// assumed.
	for _, tag := range formTag.FindAllString(body, -1) {
		action := formAction.FindStringSubmatch(tag)
		if action == nil {
			continue
		}
		method := http.MethodGet
		if m := formMethod.FindStringSubmatch(tag); m != nil {
			method = strings.ToUpper(m[1])
		}
		add(method, action[1])
	}

	// Links, as GET. A download such as /export is a plain href rather than an hx-get, because
	// swapping a JSON body into the page would be nonsense -- so a link is the only way to reach it,
	// and it would otherwise go untested.
	for _, href := range internalHref.FindAllStringSubmatch(body, -1) {
		add(http.MethodGet, href[1])
	}

	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

// probeTarget issues the request and returns the status code.
//
// A POST carries an invalid synchroniser token deliberately, so requireCSRF refuses it with 403 before
// the handler runs. That keeps the probe from mutating anything, and it is also why a 403 here is
// evidence the route exists rather than evidence of a problem.
func probeTarget(t *testing.T, endpoint, method string) int {
	t.Helper()

	switch method {
	case http.MethodGet, http.MethodHead:
		status, _, _ := get(t, endpoint, nil)
		return status
	}

	form := url.Values{"csrf": {"invalid-token-for-a-reachability-probe"}}
	status, body, _ := postForm(t, endpoint, form, nil)
	t.Logf("  probe %s %s -> %d", method, endpoint, status)
	_ = body
	return status
}
