package tests

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/services"
)

// This file covers the whole life of a private link: minting it, exchanging it for a scoped session,
// sending a suggestion back, applying it to the draft, and revoking the capability.
//
// The important assertions are not just that each step returns 200. They are that the guest cannot
// name another work, that the author's text is untouched until the author applies the suggestion,
// that applying it goes through the ordinary edit path and keeps a version, and that revocation ends
// an already-issued session. A link that merely "works" but leaks the library would be a worse
// outcome than the placeholder it replaced.

var hiddenCSRF = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func csrfFromBody(t *testing.T, body string) string {
	t.Helper()
	match := hiddenCSRF.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("no csrf hidden field in body: %s", truncate([]byte(body)))
	}
	return match[1]
}

func guestClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	// A fresh jar on the test server's transport: the recipient has no author session, but the
	// test server uses a self-signed certificate. httptest.Server.Client returns a shared client,
	// so replacing that client's jar would log the author out; build a new one with its transport.
	base := srv.Client()
	return &http.Client{Transport: base.Transport, Jar: jar}
}

func guestPost(t *testing.T, c *http.Client, endpoint string, values url.Values) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("new guest POST: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("guest POST %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

func guestGet(t *testing.T, c *http.Client, endpoint string) (int, string, http.Header) {
	t.Helper()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("new guest GET: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("guest GET %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

// mintLink drives the author's real Share endpoint and returns the raw token from the copied URL.
func mintLink(t *testing.T, srvURL, poemID string) (url string, token string) {
	t.Helper()
	status, body, _ := postForm(t, srvURL+"/share/link", urlValues("id", poemID), nil)
	if status != http.StatusOK {
		t.Fatalf("POST /share/link = %d, want 200; body: %s", status, truncate([]byte(body)))
	}
	var data struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatalf("decode link response: %v", err)
	}
	prefix := srvURL + "/s/"
	if !strings.HasPrefix(data.URL, prefix) {
		t.Fatalf("link URL = %q, want the authoring origin %q", data.URL, prefix)
	}
	return data.URL, strings.TrimPrefix(data.URL, prefix)
}

func urlValues(key, value string) url.Values {
	v := url.Values{}
	v.Set(key, value)
	return v
}

// expireLink advances the application clock past the link's expiry, so an expiry test does not have
// to wait eight hours or sleep. It exercises the same seam the rest of the application uses rather
// than editing the row: every expiry check now takes the application instant as a bound parameter.
func expireLink(t *testing.T, poemID string) {
	t.Helper()
	links, err := services.ListPrivateLinks(t.Context(), poemID)
	if err != nil {
		t.Fatalf("list links: %v", err)
	}
	if len(links) == 0 {
		t.Fatal("no link to expire")
	}
	restore := clock.SetNow(func() time.Time { return links[0].ExpiresAt.Add(time.Minute) })
	t.Cleanup(restore)
}

func TestPrivateLinkFlowFromMintToAppliedProposal(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	const original = "A Draft Work\n\nThe author's original body."
	const suggestion = "A Draft Work\n\nA reader's suggested rewrite."
	id := insertPoem(t, original)

	srv := newTestServer(t)
	defer srv.Close()

	linkURL, token := mintLink(t, srv.URL, id)
	if token == "" {
		t.Fatal("the minted link has no token")
	}

	// The recipient opens the link with no session at all.
	guest := guestClient(t, srv)
	entryURL := srv.URL + "/s/" + token
	status, _, headers := guestGet(t, guest, entryURL)
	if status != http.StatusSeeOther {
		t.Fatalf("GET the private link = %d, want 303", status)
	}
	if headers.Get("Cache-Control") != "no-store" {
		t.Errorf("entry Cache-Control = %q, want no-store", headers.Get("Cache-Control"))
	}
	if headers.Get("X-Robots-Tag") == "" {
		t.Error("entry has no X-Robots-Tag")
	}
	if got := headers.Get("Location"); got != "/guest/edit" {
		t.Errorf("redirect = %q, want /guest/edit", got)
	}

	// The limited editor shows only this work and carries its own CSRF token.
	status, editor, headers := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("GET /guest/edit = %d, want 200; body: %s", status, truncate([]byte(editor)))
	}
	if headers.Get("Cache-Control") != "no-store" || !strings.Contains(headers.Get("X-Robots-Tag"), "noindex") {
		t.Errorf("guest editor lacks privacy headers: %v", headers)
	}
	if !strings.Contains(editor, "original body") {
		t.Error("guest editor does not show the linked work")
	}
	if !strings.Contains(editor, `name="name"`) || !strings.Contains(editor, `name="content"`) {
		t.Error("guest editor lacks the optional name or the content field")
	}
	for _, leaked := range []string{`/library`, `/export`, `/dashboard`, `htmx.min.js`, `share.js`} {
		if strings.Contains(editor, leaked) {
			t.Errorf("guest editor leaks authoring surface %q", leaked)
		}
	}

	csrf := csrfFromBody(t, editor)
	form := url.Values{"csrf": {csrf}, "name": {"  Ada  "}, "content": {suggestion}}
	status, sent, _ := guestPost(t, guest, srv.URL+"/guest/propose", form)
	if status != http.StatusOK {
		t.Fatalf("POST /guest/propose = %d, want 200; body: %s", status, truncate([]byte(sent)))
	}
	if !strings.Contains(sent, "Ada") {
		t.Error("confirmation does not name the guest")
	}

	// The author's work is unchanged until the author decides.
	poem, err := services.GetPoem(t.Context(), id)
	if err != nil {
		t.Fatalf("read the work: %v", err)
	}
	if poem.Content != original {
		t.Fatalf("the guest's suggestion was applied automatically: %q", poem.Content)
	}

	proposals, err := services.ListWorkProposals(t.Context(), id)
	if err != nil {
		t.Fatalf("list proposals: %v", err)
	}
	if len(proposals) != 1 || proposals[0].GuestName != "Ada" || proposals[0].Content != suggestion {
		t.Fatalf("proposals = %+v, want one from Ada with the suggestion", proposals)
	}

	// The Share data reports the suggestion, and the history shows it distinctly.
	status, body, _ := get(t, srv.URL+"/share/work/"+id, nil)
	var shareData struct {
		Links []struct {
			ProposalCount int `json:"proposalCount"`
		} `json:"links"`
	}
	_ = json.Unmarshal([]byte(body), &shareData)
	if status != http.StatusOK || len(shareData.Links) != 1 || shareData.Links[0].ProposalCount != 1 {
		t.Errorf("share data does not report the suggestion: %s", truncate([]byte(body)))
	}

	status, history, _ := get(t, srv.URL+"/poem/"+id+"/history", nil)
	if status != http.StatusOK {
		t.Fatalf("GET history = %d", status)
	}
	for _, want := range []string{"Suggestions from private links", "Ada", "suggested rewrite", "Apply to draft"} {
		if !strings.Contains(history, want) {
			t.Errorf("history is missing %q", want)
		}
	}

	// Applying is an author action and goes through the ordinary edit path.
	apply := url.Values{"id": {id}, "proposal": {proposals[0].ID}}
	status, _, _ = postForm(t, srv.URL+"/poem/proposal/apply", apply, nil)
	if status != http.StatusNoContent {
		t.Fatalf("POST apply = %d, want 204", status)
	}
	poem, err = services.GetPoem(t.Context(), id)
	if err != nil {
		t.Fatalf("read the work after applying: %v", err)
	}
	if poem.Content != suggestion {
		t.Errorf("applied content = %q, want the suggestion", poem.Content)
	}
	versions, err := services.ListPoemVersions(t.Context(), id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) == 0 || versions[0].Content != original {
		t.Errorf("applying did not retain the original text as a version: %+v", versions)
	}

	// Revoking ends the session that was already issued.
	revoke := url.Values{"id": {id}, "link": {proposals[0].LinkID}}
	status, _, _ = postForm(t, srv.URL+"/share/link/revoke", revoke, nil)
	if status != http.StatusOK {
		t.Fatalf("POST revoke = %d, want 200", status)
	}
	status, _, _ = guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusNotFound {
		t.Errorf("revoked guest session still opens the editor: %d", status)
	}
	status, _, _ = guestGet(t, guest, entryURL)
	if status != http.StatusNotFound {
		t.Errorf("revoked link still issues a session: %d", status)
	}
	if linkURL == "" {
		t.Error("the minted link URL was empty")
	}
}

func TestPrivateLinkRefusesUnknownRevokedAndExpiredTokens(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Work\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()

	guest := guestClient(t, srv)
	status, _, _ := guestGet(t, guest, srv.URL+"/s/not-a-real-token")
	if status != http.StatusNotFound {
		t.Errorf("unknown token = %d, want 404", status)
	}

	_, token := mintLink(t, srv.URL, id)
	// Force expiry in the past through the row, the same state a real eight-hour wait produces.
	expireLink(t, id)
	status, _, _ = guestGet(t, guest, srv.URL+"/s/"+token)
	if status != http.StatusNotFound {
		t.Errorf("expired token = %d, want 404", status)
	}
}

func TestGuestSessionIsScopedToOneWork(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	first := insertPoem(t, "First Work\n\nFirst body.")
	second := insertPoem(t, "Second Work\n\nSecond body.")
	srv := newTestServer(t)
	defer srv.Close()

	_, token := mintLink(t, srv.URL, first)
	guest := guestClient(t, srv)
	status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token)
	if status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}

	// A guest credential cannot reach an author surface.
	for _, path := range []string{"/library", "/dashboard", "/export", "/share"} {
		status, _, _ = guestGet(t, guest, srv.URL+path)
		if status == http.StatusOK {
			t.Errorf("guest reached author surface %s", path)
		}
	}

	status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK || strings.Contains(editor, "Second Work") {
		t.Errorf("guest editor shows the wrong work: %s", truncate([]byte(editor)))
	}

	// A crafted form cannot rename the work: the proposal is stored against the session's work.
	form := url.Values{
		"csrf":    {csrfFromBody(t, editor)},
		"name":    {"A Reader"},
		"content": {"A suggested rewrite of the first work."},
		"id":      {second},
		"poem_id": {second},
	}
	status, _, _ = guestPost(t, guest, srv.URL+"/guest/propose", form)
	if status != http.StatusOK {
		t.Fatalf("propose = %d", status)
	}
	firstProposals, err := services.ListWorkProposals(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	secondProposals, err := services.ListWorkProposals(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstProposals) != 1 || len(secondProposals) != 0 {
		t.Errorf("a crafted id redirected the proposal: first=%d second=%d", len(firstProposals), len(secondProposals))
	}
}

func TestGuestCSRFIsRequiredAndSeparateFromAuthorCSRF(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Work\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()

	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token)
	if status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}

	// No CSRF token at all is refused.
	status, _, _ = guestPost(t, guest, srv.URL+"/guest/propose", urlValues("content", "No token."))
	if status != http.StatusForbidden {
		t.Errorf("proposal without CSRF = %d, want 403", status)
	}

	// The author's CSRF token, known to the logged-in client, does not work on the guest route.
	authorToken := csrfFromJar(t, srv.URL)
	status, _, _ = guestPost(t, guest, srv.URL+"/guest/propose", url.Values{
		"csrf": {authorToken}, "content": {"Wrong token."},
	})
	if status != http.StatusForbidden {
		t.Errorf("author CSRF accepted on the guest route: %d", status)
	}

	// A guest cookie cannot reach an author POST either.
	status, _, _ = postForm(t, srv.URL+"/poem/publish", url.Values{"id": {id}, "publish": {"1"}}, nil)
	if status != http.StatusOK {
		t.Fatalf("author publish = %d, want 200", status)
	}
	status, _, _ = guestPost(t, guest, srv.URL+"/poem/publish", url.Values{
		"csrf": {authorToken}, "id": {id}, "publish": {"1"},
	})
	if status != http.StatusSeeOther && status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Errorf("guest posting to an author route = %d", status)
	}
}

// TestGuestSessionExpiresWithTheLink pins the eight-hour boundary without waiting eight hours.
func TestGuestSessionExpiresWithTheLink(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Work\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()

	_, token := mintLink(t, srv.URL, id)
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, srv.URL+"/s/"+token); status != http.StatusSeeOther {
		t.Fatalf("entry = %d", status)
	}
	status, editor, _ := guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusOK {
		t.Fatalf("editor = %d", status)
	}
	if !strings.Contains(editor, "minutes") || !strings.Contains(editor, "closes at") {
		t.Errorf("the editor does not show the expiry window: %s", truncate([]byte(editor)))
	}

	expireLink(t, id)
	status, _, _ = guestGet(t, guest, srv.URL+"/guest/edit")
	if status != http.StatusNotFound {
		t.Errorf("an expired session still opens the editor: %d", status)
	}
}

// A byte of JavaScript in the browser must come from a .ts file, except for the one vendored htmx
// runtime. This is the guard on the JS-to-TS port: the four bundles are compiled from web/ts, and
// nothing else is allowed to appear in a served asset directory.
func TestEveryAuthoredBrowserScriptHasATypeScriptSource(t *testing.T) {
	root := repoRoot(t)
	if err := checkTSBundles(root); err != nil {
		t.Error(err)
	}
}

// checkTSBundles is separated from the test so the negative case can be exercised against a
// synthetic tree. A guard nobody has ever seen fail is not a guard.
func checkTSBundles(root string) error {
	manifest, err := os.ReadFile(filepath.Join(root, "scripts", "build_js.ts"))
	if err != nil {
		return err
	}

	outputs := regexp.MustCompile(`source:\s*"([^"]+)"[\s\S]*?output:\s*"([^"]+)"`).FindAllStringSubmatch(string(manifest), -1)
	if len(outputs) != 4 {
		return fmt.Errorf("found %d source/output pairs in build_js.ts, want 4", len(outputs))
	}

	authored := make(map[string]bool, len(outputs))
	var problems []string
	for _, pair := range outputs {
		source, output := pair[1], pair[2]
		if !strings.HasSuffix(source, ".ts") {
			problems = append(problems, fmt.Sprintf("bundle source %q is not TypeScript", source))
		}
		if _, err := os.Stat(filepath.Join(root, source)); err != nil {
			problems = append(problems, fmt.Sprintf("bundle source %q is missing", source))
		}
		if _, err := os.Stat(filepath.Join(root, output)); err != nil {
			problems = append(problems, fmt.Sprintf("compiled bundle %q is missing", output))
		}
		authored[output] = true
	}

	for _, dir := range []string{"static/js", "internal/publish/assets"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".js") {
				continue
			}
			rel := dir + "/" + name
			if authored[rel] || rel == "static/js/htmx.min.js" {
				continue
			}
			problems = append(problems, fmt.Sprintf("%s is a JavaScript file with no TypeScript source", rel))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
