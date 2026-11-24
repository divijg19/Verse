package tests

import (
	"context"
	"encoding/json"
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
