package tests

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/divijg19/Verse/internal/database"
)

// This file exists because v0.3.7 shipped an editor that could not create a poem, and nothing
// noticed for eleven releases.
//
// @CSRFField(ctx) -- the hidden synchroniser token the mutating routes require -- was nested inside
// the @If(poemID != "") guard, grouped with the id field it was written next to. Both forms were
// affected. For a new work poemID is "", because templ.Editor calls EditorScreen(ctx, "Write", "", ""),
// so the field was not rendered at all, and the POST to /poem was refused with 403 by the CSRF
// middleware before it ever reached SavePoemHandler. Editing an existing work was fine, because
// poemID is non-empty on that path, which is presumably why it read as working.
//
// htmx does not swap a non-2xx response, so the refusal produced no error, no confirmation and no
// message of any kind. The author pressed Save and nothing happened.
//
// It shipped because of the helper in test_helpers_test.go, and that is the part worth remembering.
// postForm attaches the session-bound CSRF token before every mutating request, and its comment
// claimed it did so "exactly as the rendered form does". It did not: for a new work the rendered form
// had no token to attach. The one helper that posted to /poem manufactured the exact field the form
// omitted, so every test of the create path passed against a request the browser could never make.
//
// The tests below are therefore written to submit what the form actually contains. Nothing is added to
// the parsed field set, because adding anything is what hid the bug for eleven releases.

// renderedForm is one <form> element as the browser would see it: where it goes, and the fields it
// actually carries.
//
// hxPost and action are both recorded because this application uses both styles. A form with hx-post
// is submitted by htmx; a form with action and no hx-post is submitted by the browser, and the two
// have different failure modes, so a test about a control has to know which kind it is looking at.
type renderedForm struct {
	hxPost string
	action string
	// method is the HTTP method this form would submit with: taken from an htmx method attribute if
	// one is present, else from a plain method= attribute, else GET, which is what the HTML
	// specification says a form with no method attribute does.
	method   string
	mutating bool
	fields   url.Values
	ordinal  int
}

// parseForms extracts every form in a rendered page, with the fields each one carries.
//
// A real HTML parser rather than a regexp, and the reason is specific to this bug. The defect is an
// absent field, so the test has to be able to say "this form has these fields and csrf is not among
// them". A regexp that silently fails to match a tag would instead report no fields at all and pass a
// weaker assertion, which is the failure mode this file exists to eliminate.
func parseForms(t *testing.T, page string) []renderedForm {
	t.Helper()

	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse the rendered page: %v", err)
	}

	var forms []renderedForm
	ordinal := 0
	var walk func(*html.Node, *renderedForm)
	walk = func(n *html.Node, current *renderedForm) {
		if n.Type == html.ElementNode && n.Data == "form" {
			ordinal++
			collected := url.Values{}
			verb, target := htmxSubmission(n)
			next := &renderedForm{
				hxPost:  target,
				action:  attrOf(n, "action"),
				method:  effectiveMethod(verb, n),
				ordinal: ordinal,
			}
			// A form with neither an htmx method nor a plain action submits to the current URL, which
			// a form-contract test cannot meaningfully target. It is still parsed, because its fields
			// are still worth checking.
			if next.hxPost == "" && next.action == "" {
				next.action = ""
			}
			next.mutating = !isSafeMethod(next.method)
			collectFields(n, collected)
			next.fields = collected
			forms = append(forms, *next)
			current = next
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, current)
		}
	}
	walk(doc, nil)
	return forms
}

// collectFields gathers every named field under a form node.
//
// input, textarea and select are the three elements that contribute a name to a submission. A
// disabled field is skipped because a browser would not send it either, and a field with no name
// would not be sent under any name.
func collectFields(form *html.Node, into url.Values) {
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "input", "textarea", "select":
				name := attrOf(n, "name")
				_, disabled := attrMap(n)["disabled"]
				if name != "" && !disabled {
					into.Add(name, fieldValue(n))
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(form)
}

// fieldValue returns what a browser would submit for one field: the value attribute for an input, and
// the text content for a textarea.
func fieldValue(n *html.Node) string {
	if n.Data == "input" {
		value, _ := attrMap(n)["value"]
		return value
	}
	var text strings.Builder
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(n)
	return text.String()
}

// htmxSubmission returns the verb and target of a form's htmx submission, or empty strings if it has
// none. hx-get is deliberately excluded: an hx-get form does not mutate, and treating it as one would
// make every read form look as though it needed a synchroniser token.
//
// The verb is returned separately from the target because they are different things, and conflating
// them is a bug this file was born from. An earlier version of the mutating-form sweep took the method
// from the plain method= attribute alone, so a form carrying hx-post -- which has no method attribute
// at all, and therefore defaults to GET -- was classified as a safe read. Every htmx form in the
// application was skipped, and the sweep passed against a create form that could not be submitted.
func htmxSubmission(n *html.Node) (verb, target string) {
	for _, name := range []string{"post", "put", "patch", "delete"} {
		if v := attrOf(n, "hx-"+name); v != "" {
			return strings.ToUpper(name), v
		}
	}
	return "", ""
}

// effectiveMethod returns the HTTP method the form would actually be submitted with.
//
// An htmx verb wins over a plain method attribute: htmx performs the request itself and ignores it.
// A form with neither submits by GET, per the HTML specification.
func effectiveMethod(htmxVerb string, n *html.Node) string {
	if htmxVerb != "" {
		return htmxVerb
	}
	if m := attrOf(n, "method"); m != "" {
		return strings.ToUpper(m)
	}
	return http.MethodGet
}

// isSafeMethod reports whether a method is exempt from the synchroniser token.
func isSafeMethod(m string) bool {
	switch strings.ToUpper(m) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

func attrOf(n *html.Node, name string) string {
	value, _ := attrMap(n)[name]
	return value
}

func attrMap(n *html.Node) map[string]string {
	attrs := make(map[string]string, len(n.Attr))
	for _, a := range n.Attr {
		attrs[a.Key] = a.Val
	}
	return attrs
}

// createFormsOnTheEditorPage returns every editor form that creates a work, that is, every form
// posting to /poem. There are two, the base form and the full-screen overlay, and both were broken in
// the same way; the test asserts over both rather than picking one.
func createFormsOnTheEditorPage(t *testing.T, page string) []renderedForm {
	t.Helper()

	var creating []renderedForm
	for _, f := range parseForms(t, page) {
		if f.hxPost == "/poem" {
			creating = append(creating, f)
		}
	}
	return creating
}

// TestANewWorkCanBeSavedFromTheRenderedForm is the regression test for the whole class of defect.
//
// It does not post to /poem and assert a poem appeared. It renders the editor, reads the form the
// browser would read, types into the field the form offers, and submits exactly that -- and then
// asserts the request was not refused and the work exists.
func TestANewWorkCanBeSavedFromTheRenderedForm(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	page := getBody(t, srv.URL+"/editor")
	forms := createFormsOnTheEditorPage(t, page)
	if len(forms) == 0 {
		t.Fatal("the editor rendered no form posting to /poem, so a new work has no create path at all")
	}

	for _, form := range forms {
		// The assertion that names the bug, stated before anything is submitted so the failure
		// says what is wrong rather than surfacing later as a puzzling 403.
		if _, ok := form.fields["csrf"]; !ok {
			t.Errorf("editor form %d (hx-post %q) rendered no csrf field; the create path is "+
				"refused with 403 and htmx shows the author nothing. It carries: %v",
				form.ordinal, form.hxPost, form.fields)
			continue
		}
		if _, ok := form.fields["content"]; !ok {
			t.Errorf("editor form %d carries no content field, so a new work has nowhere to be typed",
				form.ordinal)
			continue
		}
	}

	// Submitted from the first create form, with the content field filled in as typing would. The
	// field set is not augmented: adding the token here is precisely the mistake that let the bug
	// ship, so it is done nowhere in this file.
	if len(forms) == 0 {
		return
	}
	submitted := url.Values{}
	for k, vs := range forms[0].fields {
		submitted[k] = append([]string(nil), vs...)
	}
	submitted.Set("content", "a work written through the rendered form")

	resp := doPostForm(t, authClient, srv.URL+forms[0].hxPost, submitted)
	body := readBody(t, resp)
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		t.Fatalf("submitting the editor's own fields was refused with 403 (%q). The form is missing "+
			"something the route requires, and htmx does not swap a non-2xx response, so this would "+
			"look to the author like nothing happening at all", body)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("saving a new work returned %d, want 200. body: %s", resp.StatusCode, truncate([]byte(body)))
	}
	if !strings.Contains(body, "Bloom recorded") {
		t.Fatalf("the save was not acknowledged; body: %s", truncate([]byte(body)))
	}

	// The handler returns a 200 and a cheerful message whether or not a row exists, so the assertion
	// that matters is the row.
	var count int
	if err := database.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM poems WHERE content = $1`, "a work written through the rendered form").Scan(&count); err != nil {
		t.Fatalf("count the saved poem: %v", err)
	}
	if count != 1 {
		t.Fatalf("%d poem(s) carry the submitted content, want 1; the form was accepted but nothing "+
			"was stored", count)
	}
}

// TestTheEditorsCreateFormIsNotMissingARequiredField states the same requirement as a contract, so
// the next field added to a handler without a matching input is caught the same way.
func TestTheEditorsCreateFormIsNotMissingARequiredField(t *testing.T) {
	connectTestDB(t)
	srv := newTestServer(t)

	// SavePoemHandler reads exactly two things: the content, and the synchroniser token the router
	// requires before the handler runs. A create form missing either is broken, and the two failure
	// modes are different -- a missing token is a silent 403, a missing content field is a validation
	// error -- so they are named separately.
	required := map[string]string{
		"csrf":    "the router's CSRF middleware refuses the request without it, and htmx shows nothing",
		"content": "SavePoemHandler reads it and writes a validation error without it",
	}

	forms := createFormsOnTheEditorPage(t, getBody(t, srv.URL+"/editor"))
	if len(forms) == 0 {
		t.Fatal("the editor rendered no form posting to /poem")
	}

	for _, form := range forms {
		for field, consequence := range required {
			if _, ok := form.fields[field]; !ok {
				t.Errorf("editor form %d (hx-post %q) is missing the %q field: %s",
					form.ordinal, form.hxPost, field, consequence)
			}
		}
	}
}

// getBody performs an authenticated GET and returns the body as a string. The endpoint is a full
// URL, because these tests run against a per-test TLS server rather than a shared base address.
func getBody(t *testing.T, endpoint string) string {
	t.Helper()

	resp := doGet(t, authClient, endpoint)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s returned %d, want 200", endpoint, resp.StatusCode)
	}
	return readBody(t, resp)
}

// readBody reads and closes a response body.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(body)
}
