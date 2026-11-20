package tests

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/handlers"
	"github.com/divijg19/Verse/internal/services"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

// rawGet makes a request with an explicit Cookie header and no jar, so a token from one surface can
// be tried against another without the client silently adding a different credential.
func rawGet(t *testing.T, srv *httptest.Server, path, cookie string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	c := &http.Client{Transport: srv.Client().Transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func guestPostWithOrigin(t *testing.T, c *http.Client, endpoint string, values url.Values, origin string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", origin)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// These tests pin the properties the audit found missing. Each one corresponds to a way the
// implementation could regress without breaking the happy path.

// proposalFor mints a link, exchanges it and submits one suggestion, returning the work and proposal
// ids. The concurrency and no-op tests below need a proposal but not a browser-shaped flow.
func proposalFor(t *testing.T, srv *httptest.Server, content, suggestion string) (string, string) {
	t.Helper()
	id := insertPoem(t, content)
	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}
	status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("editor = %d", status)
	}
	if status, _, _ = guestPost(t, guest, srv.URL+"/guest/propose", url.Values{
		"csrf": {csrfFromBody(t, editor)}, "content": {suggestion},
	}); status != http.StatusOK {
		t.Fatalf("propose = %d", status)
	}
	proposals, err := services.ListWorkProposals(t.Context(), id)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("proposals = %+v, err = %v", proposals, err)
	}
	return id, proposals[0].ID
}

func TestConcurrentAppliesWriteOnce(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	original := "A Work\n\nThe original body."
	id, proposalID := proposalFor(t, srv, original, "A Work\n\nThe suggested body.")

	const workers = 8
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- services.ApplyWorkProposal(t.Context(), id, proposalID)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	wins, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, services.ErrNoProposal):
			conflicts++
		default:
			t.Errorf("unexpected apply error: %v", err)
		}
	}
	if wins != 1 || conflicts != workers-1 {
		t.Errorf("applies: %d succeeded, %d conflicted; want 1 and %d", wins, conflicts, workers-1)
	}

	versions, err := services.ListPoemVersions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Content != original {
		t.Errorf("concurrent apply wrote %d versions: %+v", len(versions), versions)
	}
}

func TestApplyingAnIdenticalSuggestionRecordsNoVersion(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	original := "A Work\n\nThe original body."
	// A submit with identical text is now refused, so construct the unchanged proposal directly
	// through the service. This test is about the apply step's version rule, not the submit gate.
	id, _ := proposalFor(t, srv, original, original+"\nA line the guest added.")
	proposal, err := services.ListWorkProposals(t.Context(), id)
	if err != nil || len(proposal) != 1 {
		t.Fatalf("proposals = %+v, err = %v", proposal, err)
	}
	if _, err := database.Pool.Exec(t.Context(),
		`UPDATE work_proposals SET content = $2 WHERE id = $1`, proposal[0].ID, original); err != nil {
		t.Fatalf("make the proposal identical: %v", err)
	}
	proposalID := proposal[0].ID

	if err := services.ApplyWorkProposal(t.Context(), id, proposalID); err != nil {
		t.Fatalf("apply: %v", err)
	}
	versions, err := services.ListPoemVersions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Errorf("an identical suggestion created %d versions", len(versions))
	}
	proposals, err := services.ListWorkProposals(t.Context(), id)
	if err != nil || len(proposals) != 1 || proposals[0].AppliedAt == nil {
		t.Errorf("the proposal was not marked applied: %+v, err = %v", proposals, err)
	}
}

func TestPrivateLinkUsesTheAuthorOriginNotTheReaderOrigin(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	// A reader origin is configured, as it would be in production. It must not appear in a private
	// link: /s/{token} is an authoring route and the reader's static site has no such path.
	t.Setenv("PUBLIC_BASE_URL", "https://reader.example")
	id := insertPoem(t, "A Work\n\nIts body.")

	form := url.Values{"id": {id}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/share/link", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "author.example"
	rec := httptest.NewRecorder()
	handlers.ShareLinkHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, truncate(rec.Body.Bytes()))
	}
	var data struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(data.URL, "http://author.example/s/") {
		t.Errorf("link URL = %q, want the authoring origin", data.URL)
	}
	if strings.Contains(data.URL, "reader.example") {
		t.Errorf("link URL leaked the reader origin: %q", data.URL)
	}

	// Render terminates TLS: r.TLS is nil and the original scheme arrives in X-Forwarded-Proto.
	// A client-supplied "http" in front of an honest "https" must not downgrade the capability.
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/share/link", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "http, https")
	req.Host = "author.example"
	rec = httptest.NewRecorder()
	handlers.ShareLinkHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("proxied status = %d, want 200; body: %s", rec.Code, truncate(rec.Body.Bytes()))
	}
	var proxied struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &proxied); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(proxied.URL, "https://author.example/s/") {
		t.Errorf("proxied link = %q, want https on the authoring origin", proxied.URL)
	}
}

func TestPrivateLinkRefusesAMalformedHost(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)
	id := insertPoem(t, "A Work\n\nIts body.")

	form := url.Values{"id": {id}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/share/link", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "evil@example.com"
	rec := httptest.NewRecorder()
	handlers.ShareLinkHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", rec.Code, truncate(rec.Body.Bytes()))
	}
	links, err := services.ListPrivateLinks(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Errorf("a refused request minted %d links", len(links))
	}
}

func TestGuestDraftPersistsAndIsPerLink(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	const original = "A Work\n\nThe original body."
	id := insertPoem(t, original)
	srv := newTestServer(t)
	_, firstToken := mintLink(t, srv.URL, id)
	_, secondToken := mintLink(t, srv.URL, id)

	first := guestClient(t, srv)
	if status, _, _ := guestGet(t, first, srv.URL+"/s/"+firstToken); status != http.StatusSeeOther {
		t.Fatalf("first entry = %d", status)
	}
	status, editor, _ := guestGet(t, first, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("first editor = %d", status)
	}
	csrf := csrfFromBody(t, editor)

	draft := original + "\nA line only the first guest wrote."
	status, body, _ := guestPost(t, first, srv.URL+"/guest/draft", url.Values{
		"csrf": {csrf}, "content": {draft}, "name": {"Ada"},
	})
	if status != http.StatusOK {
		t.Fatalf("autosave = %d, want 200; body: %s", status, truncate([]byte(body)))
	}
	var saved struct {
		Changed bool   `json:"changed"`
		Name    string `json:"name"`
	}
	if err := json.Unmarshal([]byte(body), &saved); err != nil {
		t.Fatalf("autosave response is not JSON: %v; body: %s", err, truncate([]byte(body)))
	}
	if !saved.Changed || saved.Name != "Ada" {
		t.Errorf("autosave = %+v, want changed with name Ada", saved)
	}

	// A reload must restore the continuous body, not the work's original text.
	status, editor, _ = guestGet(t, first, srv.URL+"/guest/edit")
	if status != http.StatusOK || !strings.Contains(editor, "A line only the first guest wrote.") {
		t.Errorf("reload did not restore the draft: %s", truncate([]byte(editor)))
	}

	// The second link to the same work is a separate conversation with its own body.
	second := guestClient(t, srv)
	if status, _, _ := guestGet(t, second, srv.URL+"/s/"+secondToken); status != http.StatusSeeOther {
		t.Fatalf("second entry = %d", status)
	}
	status, other, _ := guestGet(t, second, srv.URL+"/guest/edit")
	if status != http.StatusOK || strings.Contains(other, "A line only the first guest wrote.") {
		t.Errorf("second link inherited the first link's draft: %s", truncate([]byte(other)))
	}
}

func TestGuestCannotSendWithoutChanges(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	const original = "A Work\n\nThe original body."
	id := insertPoem(t, original)
	srv := newTestServer(t)
	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}
	status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("editor = %d", status)
	}
	status, _, _ = guestPost(t, guest, srv.URL+"/guest/propose", url.Values{
		"csrf": {csrfFromBody(t, editor)}, "content": {original},
	})
	if status != http.StatusConflict {
		t.Errorf("unchanged submit = %d, want 409", status)
	}
	proposals, err := services.ListWorkProposals(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 0 {
		t.Errorf("an unchanged submit stored %d proposals", len(proposals))
	}
}

func TestPrimarySubmissionUpdatesAndAlternativeBranches(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	const original = "A Work\n\nThe original body."
	id := insertPoem(t, original)
	srv := newTestServer(t)
	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}

	send := func(content, branch string) int {
		status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
		if status != http.StatusOK {
			t.Fatalf("editor = %d", status)
		}
		form := url.Values{"csrf": {csrfFromBody(t, editor)}, "content": {content}, "name": {"Ada"}}
		if branch != "" {
			form.Set("branch", branch)
		}
		status, body, _ := guestPost(t, guest, srv.URL+"/guest/propose", form)
		if status != http.StatusOK {
			t.Errorf("submit = %d; body: %s", status, truncate([]byte(body)))
		}
		return status
	}

	send(original+"\nFirst version.", "")
	proposals, err := services.ListWorkProposals(t.Context(), id)
	if err != nil || len(proposals) != 1 || proposals[0].Branch != services.PrimaryBranch {
		t.Fatalf("proposals after first send = %+v, err = %v", proposals, err)
	}
	firstID := proposals[0].ID

	// A second, changed primary must update the same row in place.
	send(original+"\nSecond version.", "")
	proposals, err = services.ListWorkProposals(t.Context(), id)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("proposals after update = %+v, err = %v", proposals, err)
	}
	if proposals[0].ID != firstID || !strings.Contains(proposals[0].Content, "Second version.") {
		t.Errorf("primary was not updated in place: %+v", proposals[0])
	}

	// An explicit alternative keeps the primary and adds a second branch.
	send(original+"\nAn alternative version.", "new")
	proposals, err = services.ListWorkProposals(t.Context(), id)
	if err != nil || len(proposals) != 2 {
		t.Fatalf("proposals after alternative = %+v, err = %v", proposals, err)
	}
	foundPrimary, foundAlt := false, false
	for _, proposal := range proposals {
		if proposal.ID == firstID && strings.Contains(proposal.Content, "Second version.") {
			foundPrimary = true
		}
		if strings.HasPrefix(proposal.Branch, "alt:") && strings.Contains(proposal.Content, "An alternative version.") {
			foundAlt = true
		}
	}
	if !foundPrimary || !foundAlt {
		t.Errorf("branches are wrong: %+v", proposals)
	}
}

func TestGuestBranchTabsHaveIndependentDrafts(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	const original = "A Work\n\nThe original body."
	id := insertPoem(t, original)
	srv := newTestServer(t)
	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}

	send := func(content, branch string) {
		status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
		if status != http.StatusOK {
			t.Fatalf("editor = %d", status)
		}
		form := url.Values{"csrf": {csrfFromBody(t, editor)}, "content": {content}, "name": {"Ada"}}
		if branch != "" {
			form.Set("branch", branch)
		}
		status, body, _ := guestPost(t, guest, srv.URL+"/guest/propose", form)
		if status != http.StatusOK {
			t.Fatalf("submit to %q = %d; body: %s", branch, status, truncate([]byte(body)))
		}
	}

	send(original+"\nMain version.", "")
	send(original+"\nAlternative version.", "new")
	proposals, err := services.ListWorkProposals(t.Context(), id)
	if err != nil || len(proposals) != 2 {
		t.Fatalf("proposals = %+v, err = %v", proposals, err)
	}
	altBranch := ""
	for _, proposal := range proposals {
		if strings.HasPrefix(proposal.Branch, "alt:") {
			altBranch = proposal.Branch
		}
	}
	if altBranch == "" {
		t.Fatal("no alternative branch was created")
	}

	status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("editor = %d", status)
	}
	if !strings.Contains(editor, "Main version") || !strings.Contains(editor, "Alternative 1") ||
		!strings.Contains(editor, "data-guest-branch-tab") {
		t.Errorf("branch rail is missing tabs: %s", truncate([]byte(editor)))
	}

	// Switching to the alternative shows its own body, and autosaving it leaves the main draft alone.
	status, altEditor, _ := guestGet(t, guest, srv.URL+"/guest/edit?branch="+url.QueryEscape(altBranch))
	if status != http.StatusOK || !strings.Contains(altEditor, "Alternative version.") {
		t.Fatalf("alternative editor = %d; body: %s", status, truncate([]byte(altEditor)))
	}
	if !strings.Contains(altEditor, `data-guest-branch value="`+altBranch+`"`) {
		t.Errorf("alternative editor did not select its branch: %s", truncate([]byte(altEditor)))
	}
	csrf := csrfFromBody(t, altEditor)
	status, _, _ = guestPost(t, guest, srv.URL+"/guest/draft", url.Values{
		"csrf": {csrf}, "branch": {altBranch}, "content": {original + "\nEdited alternative."},
	})
	if status != http.StatusOK {
		t.Fatalf("alternative autosave = %d", status)
	}

	status, mainEditor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK || !strings.Contains(mainEditor, "Main version.") ||
		strings.Contains(mainEditor, "Edited alternative.") {
		t.Errorf("main draft was changed by the alternative autosave: %s", truncate([]byte(mainEditor)))
	}

	// A changed alternative updates only its own submission.
	status, altEditor, _ = guestGet(t, guest, srv.URL+"/guest/edit?branch="+url.QueryEscape(altBranch))
	if status != http.StatusOK {
		t.Fatalf("alternative editor = %d", status)
	}
	status, _, _ = guestPost(t, guest, srv.URL+"/guest/propose", url.Values{
		"csrf": {csrfFromBody(t, altEditor)}, "branch": {altBranch},
		"content": {original + "\nEdited alternative."},
	})
	if status != http.StatusOK {
		t.Fatalf("alternative submit = %d", status)
	}
	proposals, err = services.ListWorkProposals(t.Context(), id)
	if err != nil || len(proposals) != 2 {
		t.Fatalf("proposals after alternative update = %+v, err = %v", proposals, err)
	}
	for _, proposal := range proposals {
		if proposal.Branch == altBranch && !strings.Contains(proposal.Content, "Edited alternative.") {
			t.Errorf("alternative proposal was not updated: %+v", proposal)
		}
		if proposal.Branch == services.PrimaryBranch && !strings.Contains(proposal.Content, "Main version.") {
			t.Errorf("primary proposal was overwritten: %+v", proposal)
		}
	}
}

func TestGuestDraftRefusedAfterRevocation(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Work\n\nIts body.")
	srv := newTestServer(t)
	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}
	status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("editor = %d", status)
	}
	links, err := services.ListPrivateLinks(t.Context(), id)
	if err != nil || len(links) != 1 {
		t.Fatalf("links = %+v, err = %v", links, err)
	}
	status, _, _ = postForm(t, srv.URL+"/share/link/revoke", url.Values{
		"id": {id}, "link": {links[0].ID},
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("revoke = %d", status)
	}
	status, _, _ = guestPost(t, guest, srv.URL+"/guest/draft", url.Values{
		"csrf": {csrfFromBody(t, editor)}, "content": {"A change after revocation."},
	})
	if status != http.StatusNotFound {
		t.Errorf("autosave after revocation = %d, want 404", status)
	}
}

func TestWhitespaceOnlyEditIsNotASubmission(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	const original = "A Work\n\nThe original body."
	id := insertPoem(t, original)
	srv := newTestServer(t)
	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}
	status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("editor = %d", status)
	}
	csrf := csrfFromBody(t, editor)

	// A trailing newline is exactly the edit an accidental keypress makes. It must not create a
	// proposal whose content is identical to the work.
	status, body, _ := guestPost(t, guest, srv.URL+"/guest/propose", url.Values{
		"csrf": {csrf}, "content": {original + "\n\n   "},
	})
	if status != http.StatusConflict {
		t.Errorf("whitespace-only submit = %d, want 409; body: %s", status, truncate([]byte(body)))
	}
	proposals, err := services.ListWorkProposals(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 0 {
		t.Errorf("whitespace-only submit stored %d proposals", len(proposals))
	}
}

func TestTSGuardFailsOnAStrayJavaScriptFile(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	manifest := `const bundles = [
  { source: "web/ts/one.ts", output: "static/js/one.js" },
  { source: "web/ts/two.ts", output: "static/js/two.js" },
  { source: "web/ts/three.ts", output: "static/js/three.js" },
  { source: "web/ts/four.ts", output: "internal/publish/assets/public.js" },
  { source: "web/ts/five.ts", output: "static/js/five.js" },
];`
	write("scripts/build_js.ts", manifest)
	for _, path := range []string{
		"web/ts/one.ts", "web/ts/two.ts", "web/ts/three.ts", "web/ts/four.ts", "web/ts/five.ts",
		"static/js/one.js", "static/js/two.js", "static/js/three.js", "static/js/five.js",
		"internal/publish/assets/public.js",
	} {
		write(path, "// fixture")
	}

	if err := checkTSBundles(root); err != nil {
		t.Fatalf("the guard rejected a clean synthetic tree: %v", err)
	}
	write("static/js/stray.js", "// hand-written")
	if err := checkTSBundles(root); err == nil {
		t.Fatal("the guard accepted a hand-written .js file")
	}
}

func TestSoftDeletedWorkRefusesItsPrivateLink(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Work\n\nIts body.")
	srv := newTestServer(t)
	linkURL, token := mintLink(t, srv.URL, id)

	if err := services.SoftDeletePoem(t.Context(), id); err != nil {
		t.Fatalf("soft-delete the work: %v", err)
	}

	guest := guestClient(t, srv)
	// Entry, editor and submit must all refuse the link. The submit path used to accept it because
	// the existence check did not join poems.deleted_at.
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusNotFound {
		t.Errorf("entry on a deleted work = %d, want 404", status)
	}
	if status, _, _ := guestGet(t, guest, srv.URL+"/guest/edit"); status != http.StatusNotFound {
		t.Errorf("editor on a deleted work = %d, want 404", status)
	}
	status, body, _ := guestPost(t, guest, srv.URL+"/guest/propose", url.Values{
		"csrf": {"not-needed"}, "content": {"A suggestion."},
	})
	if status != http.StatusNotFound {
		t.Errorf("proposal on a deleted work = %d, want 404; body: %s", status, truncate([]byte(body)))
	}
	proposals, err := services.ListWorkProposals(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 0 {
		t.Errorf("a deleted work accumulated %d proposals", len(proposals))
	}
	_ = linkURL
}

func TestApplyProposalTwiceIsRejectedAndWritesOnce(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	const original = "A Work\n\nThe original body."
	const suggestion = "A Work\n\nThe suggested body."
	id := insertPoem(t, original)
	srv := newTestServer(t)
	_, token := mintLink(t, srv.URL, id)

	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}
	status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("editor = %d", status)
	}
	if status, _, _ = guestPost(t, guest, srv.URL+"/guest/propose", url.Values{
		"csrf": {csrfFromBody(t, editor)}, "content": {suggestion},
	}); status != http.StatusOK {
		t.Fatalf("propose = %d", status)
	}

	proposals, err := services.ListWorkProposals(t.Context(), id)
	if err != nil || len(proposals) != 1 {
		t.Fatalf("proposals = %+v, err = %v", proposals, err)
	}

	apply := url.Values{"id": {id}, "proposal": {proposals[0].ID}}
	status, _, _ = postForm(t, srv.URL+"/poem/proposal/apply", apply, nil)
	if status != http.StatusNoContent {
		t.Fatalf("first apply = %d, want 204", status)
	}
	status, _, _ = postForm(t, srv.URL+"/poem/proposal/apply", apply, nil)
	if status != http.StatusConflict {
		t.Errorf("second apply = %d, want 409", status)
	}

	versions, err := services.ListPoemVersions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Content != original {
		t.Errorf("apply wrote %d versions, want one holding the original: %+v", len(versions), versions)
	}
	poem, err := services.GetPoem(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if poem.Content != suggestion {
		t.Errorf("applied content = %q, want the suggestion", poem.Content)
	}

	// Applying a proposal whose work has since been deleted is a 404, not a silent success. Use a
	// second work so the proposal really belongs to the work being named in the request.
	other := insertPoem(t, "Another Work\n\nIts body.")
	_, otherToken := mintLink(t, srv.URL, other)
	otherGuest := guestClient(t, srv)
	if s, _, _ := guestGet(t, otherGuest, srv.URL+"/s/"+otherToken); s != http.StatusSeeOther {
		t.Fatalf("second entry = %d", s)
	}
	s, otherEditor, _ := guestGet(t, otherGuest, srv.URL+"/guest/edit")
	if s != http.StatusOK {
		t.Fatalf("second editor = %d", s)
	}
	if s, _, _ = guestPost(t, otherGuest, srv.URL+"/guest/propose", url.Values{
		"csrf": {csrfFromBody(t, otherEditor)}, "content": {"Another suggestion."},
	}); s != http.StatusOK {
		t.Fatalf("second propose = %d", s)
	}
	otherProposals, err := services.ListWorkProposals(t.Context(), other)
	if err != nil || len(otherProposals) != 1 {
		t.Fatalf("second proposals = %+v, err = %v", otherProposals, err)
	}
	if err := services.SoftDeletePoem(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	status, _, _ = postForm(t, srv.URL+"/poem/proposal/apply", url.Values{"id": {other}, "proposal": {otherProposals[0].ID}}, nil)
	if status != http.StatusNotFound {
		t.Errorf("apply to a deleted work = %d, want 404", status)
	}
}

func TestGuestValidationKeepsTheValidatorStatus(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Work\n\nIts body.")
	srv := newTestServer(t)
	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}
	status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("editor = %d", status)
	}
	status, _, _ = guestPost(t, guest, srv.URL+"/guest/propose", url.Values{
		"csrf": {csrfFromBody(t, editor)}, "content": {"   "},
	})
	if status != http.StatusBadRequest {
		t.Errorf("blank proposal = %d, want 400", status)
	}
}

func TestGuestCookieIsScopedAndTokenDomainsDoNotCross(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Work\n\nIts body.")
	srv := newTestServer(t)
	_, token := mintLink(t, srv.URL, id)

	guest := guestClient(t, srv)
	status, _, headers := guestGet(t, guest, srv.URL+"/s/"+token)
	if status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}
	cookies := headers.Values("Set-Cookie")
	var guestCookie string
	for _, line := range cookies {
		if strings.HasPrefix(line, "verse_guest=") {
			guestCookie = strings.SplitN(line, ";", 2)[0]
			for _, want := range []string{"Path=/guest/", "HttpOnly", "Secure", "SameSite=Lax"} {
				if !strings.Contains(line, want) {
					t.Errorf("guest cookie is missing %s: %s", want, line)
				}
			}
		}
	}
	if guestCookie == "" {
		t.Fatalf("no guest cookie issued: %v", cookies)
	}

	// The author's session token cannot be used as a guest cookie, and vice versa. Both token
	// formats are signed with the same secret, so the HMAC domains are the only thing separating
	// them; this assertion is what keeps a future refactor from dropping that prefix.
	authorCookie := ""
	for _, c := range authClient.Jar.Cookies(mustURL(t, srv.URL)) {
		if c.Name == "verse_session" {
			authorCookie = c.Name + "=" + c.Value
		}
	}
	if authorCookie == "" {
		t.Fatal("no author session cookie")
	}
	status, _ = rawGet(t, srv, "/guest/edit", authorCookie)
	if status != http.StatusNotFound {
		t.Errorf("an author token opened the guest editor: %d", status)
	}
	status, _ = rawGet(t, srv, "/library", guestCookie)
	if status != http.StatusSeeOther && status != http.StatusFound {
		t.Errorf("a guest token opened the author library: %d", status)
	}
}

func TestGuestCannotApplyAProposal(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Work\n\nIts body.")
	srv := newTestServer(t)
	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}
	status, _, _ := guestPost(t, guest, srv.URL+"/poem/proposal/apply", url.Values{
		"id": {id}, "proposal": {"00000000-0000-0000-0000-000000000000"},
	})
	if status == http.StatusNoContent || status == http.StatusOK {
		t.Errorf("a guest applied a proposal: %d", status)
	}
}

func TestGuestCSRFRejectsCrossOrigin(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Work\n\nIts body.")
	srv := newTestServer(t)
	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}
	status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("editor = %d", status)
	}
	status, _ = guestPostWithOrigin(t, guest, srv.URL+"/guest/propose", url.Values{
		"csrf": {csrfFromBody(t, editor)}, "content": {"A suggestion."},
	}, "https://elsewhere.test")
	if status != http.StatusForbidden {
		t.Errorf("cross-origin proposal = %d, want 403", status)
	}
}
