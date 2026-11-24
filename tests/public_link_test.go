package tests

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/services"
)

func mintPublicLink(t *testing.T, srvURL, poemID string) string {
	t.Helper()
	status, body, _ := postForm(t, srvURL+"/share/public-link", urlValues("id", poemID), nil)
	if status != http.StatusOK {
		t.Fatalf("mint public link = %d; body: %s", status, truncate([]byte(body)))
	}
	var data struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(data.URL, srvURL+"/p/") {
		t.Fatalf("public link URL = %q, want authoring origin", data.URL)
	}
	return strings.TrimPrefix(data.URL, srvURL+"/p/")
}

func TestPublicLinkRecordsEverySubmission(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Public Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatalf("publish: %v", err)
	}
	srv := newTestServer(t)
	defer srv.Close()
	token := mintPublicLink(t, srv.URL, id)

	visitor := guestClient(t, srv)
	status, page, _ := guestGet(t, visitor, srv.URL+"/p/"+token)
	if status != http.StatusOK {
		t.Fatalf("public form = %d; body: %s", status, truncate([]byte(page)))
	}
	if strings.Contains(page, "verse-dashboard") || !strings.Contains(page, "Send a suggestion") {
		t.Errorf("public form carries authoring chrome or lacks the form: %s", truncate([]byte(page)))
	}
	csrf := csrfFromBody(t, page)

	for _, message := range []string{"First anonymous suggestion.", "Second anonymous suggestion."} {
		status, body, _ := guestPost(t, visitor, srv.URL+"/p/"+token+"/submit", url.Values{
			"csrf": {csrf}, "name": {"Reader"}, "content": {message},
		})
		if status != http.StatusOK {
			t.Fatalf("submit = %d; body: %s", status, truncate([]byte(body)))
		}
	}
	submissions, err := services.ListPublicSubmissions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(submissions) != 2 {
		t.Fatalf("recorded %d submissions, want 2", len(submissions))
	}
	if !strings.Contains(submissions[0].Content, "Second") || !strings.Contains(submissions[1].Content, "First") {
		t.Errorf("submissions out of order: %+v", submissions)
	}
	poem, err := services.GetPoem(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(poem.Content, "Its body.") {
		t.Errorf("public submission changed the work: %q", poem.Content)
	}
}

func TestPublicLinkRequiresPublishedWorkAndValidCSRF(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	draft := insertPoem(t, "A Draft Work\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()
	status, _, _ := postForm(t, srv.URL+"/share/public-link", urlValues("id", draft), nil)
	if status != http.StatusConflict {
		t.Errorf("public link for a draft = %d, want 409", status)
	}

	id := insertPoem(t, "A Public Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	token := mintPublicLink(t, srv.URL, id)
	visitor := guestClient(t, srv)
	status, _, _ = guestGet(t, visitor, srv.URL+"/p/"+token)
	if status != http.StatusOK {
		t.Fatalf("form = %d", status)
	}
	status, _, _ = guestPost(t, visitor, srv.URL+"/p/"+token+"/submit", url.Values{
		"csrf": {"forged"}, "content": {"A suggestion."},
	})
	if status != http.StatusForbidden {
		t.Errorf("forged CSRF = %d, want 403", status)
	}
}

func TestPublicLinkRevocationStopsSubmissions(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Public Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatalf("publish: %v", err)
	}
	srv := newTestServer(t)
	defer srv.Close()
	token := mintPublicLink(t, srv.URL, id)
	visitor := guestClient(t, srv)
	status, page, _ := guestGet(t, visitor, srv.URL+"/p/"+token)
	if status != http.StatusOK {
		t.Fatalf("form = %d", status)
	}
	csrf := csrfFromBody(t, page)
	links, err := services.ListPublicLinks(t.Context(), id)
	if err != nil || len(links) != 1 {
		t.Fatalf("links = %+v, err = %v", links, err)
	}
	status, _, _ = postForm(t, srv.URL+"/share/public-link/revoke", url.Values{
		"id": {id}, "link": {links[0].ID},
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("revoke = %d", status)
	}
	status, _, _ = guestPost(t, visitor, srv.URL+"/p/"+token+"/submit", url.Values{
		"csrf": {csrf}, "content": {"After revocation."},
	})
	if status != http.StatusNotFound {
		t.Errorf("submit after revocation = %d, want 404", status)
	}
	status, withdrawnPage, _ := guestGet(t, visitor, srv.URL+"/p/"+token)
	if status != http.StatusNotFound {
		t.Errorf("form after revocation = %d, want 404", status)
	}
	if strings.Contains(withdrawnPage, "Its body.") {
		t.Error("a revoked link still renders the work's body")
	}
}

func TestPublicEntryIsRateLimited(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Public Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.Close()
	token := mintPublicLink(t, srv.URL, id)
	visitor := guestClient(t, srv)

	limited := false
	for i := 0; i < 65; i++ {
		status, _, _ := guestGet(t, visitor, srv.URL+"/p/"+token)
		if status == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("the public entry route never returned 429")
	}
}

// TestPublicLinkStopsWorkingWhenTheWorkIsWithdrawn pins the write-time gate: unpublishing or
// soft-deleting a work must make an outstanding public URL inert on both the form and the submit
// route, even though the link row itself is retained for history.
func TestPublicLinkStopsWorkingWhenTheWorkIsWithdrawn(t *testing.T) {
	for _, tc := range []struct {
		name     string
		withdraw func(context.Context, string) error
	}{
		{"unpublish", services.UnpublishPoem},
		{"soft delete", services.SoftDeletePoem},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connectTestDB(t)
			truncatePoems(t)

			id := insertPoem(t, "A Withdrawn Work\n\nIts body.")
			if err := services.PublishPoem(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			srv := newTestServer(t)
			defer srv.Close()
			token := mintPublicLink(t, srv.URL, id)
			visitor := guestClient(t, srv)
			status, page, _ := guestGet(t, visitor, srv.URL+"/p/"+token)
			if status != http.StatusOK {
				t.Fatalf("form = %d", status)
			}
			csrf := csrfFromBody(t, page)

			if err := tc.withdraw(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			if status, _, _ := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+token); status != http.StatusNotFound {
				t.Errorf("form after %s = %d, want 404", tc.name, status)
			}
			if status, _, _ := guestPost(t, visitor, srv.URL+"/p/"+token+"/submit", url.Values{
				"csrf": {csrf}, "content": {"After withdrawal."},
			}); status != http.StatusNotFound {
				t.Errorf("submit after %s = %d, want 404", tc.name, status)
			}
		})
	}
}

// TestPublicCarouselShowsOnlyTheLinkedWork pins the v0.5.14 surface contract: the anonymous reader
// gets the linked work's full title and body as a read-only slide, the unchanged form as a second
// slide, and nothing that belongs to the author or to another work.
func TestPublicCarouselShowsOnlyTheLinkedWork(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "The Linked Work\n\nA unique body sentinel.\nA second line.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	other := insertPoem(t, "An Unrelated Work\n\nOTHER WORK SENTINEL.")
	if err := services.PublishPoem(t.Context(), other); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t)
	defer srv.Close()
	token := mintPublicLink(t, srv.URL, id)
	visitor := guestClient(t, srv)
	status, page, headers := guestGet(t, visitor, srv.URL+"/p/"+token)
	if status != http.StatusOK {
		t.Fatalf("public page = %d; body: %s", status, truncate([]byte(page)))
	}

	for _, needle := range []string{
		`data-copy-title>The Linked Work</h1>`,
		`data-copy-body>A unique body sentinel.`,
		`name="verse-public-view"`,
		`id="verse-public-read" class="verse-public-radio" checked`,
		`id="verse-public-write" class="verse-public-radio"`,
		`id="verse-public-read-panel" class="verse-public-panel"`,
		`id="verse-public-write-panel" class="verse-public-panel"`,
		`role="region"`,
		`aria-labelledby="verse-public-read-label"`,
		`aria-labelledby="verse-public-write-label"`,
		`data-public-copy-text`,
		`data-public-actions hidden`,
		`data-public-copy-hint`,
		`<script src="/static/js/public_submit.js" defer>`,
		`name="csrf" value="`,
	} {
		if !strings.Contains(page, needle) {
			t.Errorf("public page is missing %q", needle)
		}
	}
	for _, needle := range []string{
		"OTHER WORK SENTINEL",
		"An Unrelated Work",
		"verse-dashboard",
		"verse-desktop-nav",
		"hx-get", "hx-swap", "hx-target", "htmx.min.js",
		`data-share-address`,
		`data-copy-url="/p/`,
		"verse_session=", "verse_guest=", "verse_csrf=",
	} {
		if strings.Contains(page, needle) {
			t.Errorf("public page leaks %q", needle)
		}
	}

	if headers.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", headers.Get("Cache-Control"))
	}
	if !strings.Contains(headers.Get("X-Robots-Tag"), "noindex") {
		t.Errorf("X-Robots-Tag = %q, want noindex", headers.Get("X-Robots-Tag"))
	}
	if !strings.Contains(headers.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Errorf("CSP = %q, want pinned script-src", headers.Get("Content-Security-Policy"))
	}
	if headers.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("Referrer-Policy = %q, want no-referrer", headers.Get("Referrer-Policy"))
	}
}

// TestPublicCarouselCopiesTheUntruncatedTitle pins the reason /p uses DisplayTitle rather than the
// width-limited WorkTitle: an ellipsis in the heading would land on the clipboard too.
func TestPublicCarouselCopiesTheUntruncatedTitle(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	long := strings.Repeat("T", 100)
	id := insertPoem(t, long+"\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.Close()
	token := mintPublicLink(t, srv.URL, id)

	status, page, _ := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+token)
	if status != http.StatusOK {
		t.Fatalf("public page = %d", status)
	}
	if !strings.Contains(page, `data-copy-title>`+long) {
		t.Error("the public page truncates the title it offers to copy")
	}
}

// TestPublicCarouselEscapesTheWork covers the new read-only slide with hostile content. Templ
// escapes both text nodes and attributes, and the copy script reads textContent, so the clipboard
// receives the original text while the browser renders no executable markup.
func TestPublicCarouselEscapesTheWork(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	// Literal payloads, not hostileInputs[n]: this test must not change meaning when another file
	// reorders its slice. Both the title and the body are asserted inside the read slide so the test
	// cannot pass on the <head><title> alone or on an empty body.
	title := `"><script>alert(1)</script>`
	body := `"><img src=x onerror=alert(1)>`
	id := insertPoem(t, title+"\n\n"+body)
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.Close()
	token := mintPublicLink(t, srv.URL, id)

	status, page, _ := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+token)
	if status != http.StatusOK {
		t.Fatalf("public page = %d", status)
	}
	for _, needle := range []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
	} {
		if strings.Contains(page, needle) {
			t.Errorf("public page renders executable markup %q", needle)
		}
	}
	for _, needle := range []string{
		`data-copy-title>&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;</h1>`,
		`data-copy-body>&#34;&gt;&lt;img src=x onerror=alert(1)&gt;</div>`,
	} {
		if !strings.Contains(page, needle) {
			t.Errorf("public page does not carry escaped hostile text in the read slide: %q", needle)
		}
	}
}

// TestGetPublicLinkPoemRefusesWithdrawnWork pins the single-statement read: a link whose work was
// unpublished, soft-deleted or revoked must fail before any body can be rendered.
func TestGetPublicLinkPoemRefusesWithdrawnWork(t *testing.T) {
	for _, tc := range []struct {
		name     string
		withdraw func(context.Context, string) error
	}{
		{"unpublish", services.UnpublishPoem},
		{"soft delete", services.SoftDeletePoem},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connectTestDB(t)
			truncatePoems(t)
			id := insertPoem(t, "A Withdrawn Work\n\nIts body.")
			if err := services.PublishPoem(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			srv := newTestServer(t)
			defer srv.Close()
			token := mintPublicLink(t, srv.URL, id)

			if _, poem, err := services.GetPublicLinkPoem(t.Context(), token); err != nil {
				t.Fatalf("read before withdrawal: %v", err)
			} else if !strings.Contains(poem.Content, "Its body.") {
				t.Fatalf("read returned the wrong work: %q", poem.Content)
			}
			if err := tc.withdraw(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			if _, _, err := services.GetPublicLinkPoem(t.Context(), token); !errors.Is(err, services.ErrNoPublicLink) {
				t.Errorf("read after %s = %v, want ErrNoPublicLink", tc.name, err)
			}
		})
	}
	t.Run("revoke", func(t *testing.T) {
		connectTestDB(t)
		truncatePoems(t)
		id := insertPoem(t, "A Revoked Work\n\nIts body.")
		if err := services.PublishPoem(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		srv := newTestServer(t)
		defer srv.Close()
		token := mintPublicLink(t, srv.URL, id)
		links, err := services.ListPublicLinks(t.Context(), id)
		if err != nil || len(links) != 1 {
			t.Fatalf("links = %+v, err = %v", links, err)
		}
		if err := services.RevokePublicLink(t.Context(), id, links[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := services.GetPublicLinkPoem(t.Context(), token); !errors.Is(err, services.ErrNoPublicLink) {
			t.Errorf("read after revoke = %v, want ErrNoPublicLink", err)
		}
	})
}
