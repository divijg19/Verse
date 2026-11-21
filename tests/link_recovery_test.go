package tests

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/services"
)

type shareLinkRow struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Revoked bool   `json:"revoked"`
}

func shareLinkPayload(t *testing.T, srvURL, poemID string) struct {
	Links       []shareLinkRow `json:"links"`
	PublicLinks []shareLinkRow `json:"publicLinks"`
} {
	t.Helper()
	status, body, _ := get(t, srvURL+"/share/work/"+poemID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /share/work/%s = %d; body: %s", poemID, status, truncate([]byte(body)))
	}
	var out struct {
		Links       []shareLinkRow `json:"links"`
		PublicLinks []shareLinkRow `json:"publicLinks"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRecoveredLinkURLsSurviveReload is the regression guard for the original defect: a link row
// minted before this page session must still carry a Copy URL in the Share payload.
func TestRecoveredLinkURLsSurviveReload(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Recoverable Work\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()

	privateURL, _ := mintLink(t, srv.URL, id)
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatalf("publish: %v", err)
	}
	publicToken := mintPublicLink(t, srv.URL, id)

	// A fresh request with no client-side state stands in for a reload.
	payload := shareLinkPayload(t, srv.URL, id)
	if len(payload.Links) != 1 || payload.Links[0].URL != privateURL {
		t.Fatalf("private links = %+v, want one row with URL %q", payload.Links, privateURL)
	}
	publicURL := srv.URL + "/p/" + publicToken
	if len(payload.PublicLinks) != 1 || payload.PublicLinks[0].URL != publicURL {
		t.Fatalf("public links = %+v, want one row with URL %q", payload.PublicLinks, publicURL)
	}

	// The recovered URLs are real capabilities, not merely stable strings.
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, privateURL); status != http.StatusSeeOther {
		t.Errorf("recovered private URL = %d, want 303 redirect into the guest editor", status)
	}
	if status, _, _ := guestGet(t, guest, publicURL); status != http.StatusOK {
		t.Errorf("recovered public URL = %d, want 200", status)
	}
}

// TestLegacyLinkHasNoCopyURL pins the backfill decision: a row with a NULL seed cannot be recovered
// and must fail closed, without hiding the other links on the work.
func TestLegacyLinkHasNoCopyURL(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Legacy Work\n\nIts body.")
	now := time.Now().UTC()
	_, err := database.Pool.Exec(t.Context(), `
        INSERT INTO private_links (id, poem_id, token_hash, created_at, expires_at)
        VALUES ($1, $2, $3, $4, $5)`,
		uuid.NewString(), id, strings.Repeat("0", 64), now, now.Add(8*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t)
	defer srv.Close()
	payload := shareLinkPayload(t, srv.URL, id)
	if len(payload.Links) != 1 {
		t.Fatalf("legacy link was filtered out: %+v", payload.Links)
	}
	if payload.Links[0].URL != "" {
		t.Errorf("legacy row got URL %q, want none", payload.Links[0].URL)
	}
}

// TestPublicReMintWithSubmissionKeepsIdentity reproduces the FK violation that made a second Public
// Link click return 500 once a submission existed. The row id and its submissions must survive.
func TestPublicReMintWithSubmissionKeepsIdentity(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Submitted Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatalf("publish: %v", err)
	}
	srv := newTestServer(t)
	defer srv.Close()

	token := mintPublicLink(t, srv.URL, id)
	first := shareLinkPayload(t, srv.URL, id)
	if len(first.PublicLinks) != 1 {
		t.Fatalf("first payload = %+v", first.PublicLinks)
	}
	wantID := first.PublicLinks[0].ID

	guest := guestClient(t, srv)
	status, page, _ := guestGet(t, guest, srv.URL+"/p/"+token)
	if status != http.StatusOK {
		t.Fatalf("public form = %d", status)
	}
	status, _, _ = guestPost(t, guest, srv.URL+"/p/"+token+"/submit", url.Values{
		"csrf": {csrfFromBody(t, page)}, "name": {"Reader"}, "content": {"A suggestion."},
	})
	if status != http.StatusOK {
		t.Fatalf("submit = %d", status)
	}

	status, body, _ := postForm(t, srv.URL+"/share/public-link", urlValues("id", id), nil)
	if status != http.StatusOK {
		t.Fatalf("re-mint with a submission = %d; body: %s", status, truncate([]byte(body)))
	}
	var again shareLinkRow
	if err := json.Unmarshal([]byte(body), &again); err != nil {
		t.Fatal(err)
	}
	if again.ID != wantID {
		t.Errorf("re-mint changed the link id: got %q, want %q", again.ID, wantID)
	}
	// Re-mint deliberately rotates the token while keeping the row id, which is what preserves the
	// submission rows and their attribution. Idempotent return is a later release.
	if !strings.HasPrefix(again.URL, srv.URL+"/p/") || again.URL == srv.URL+"/p/"+token {
		t.Errorf("re-mint did not rotate the token: %q", again.URL)
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+token); status != http.StatusNotFound {
		t.Errorf("old public token still resolves: %d", status)
	}

	second := shareLinkPayload(t, srv.URL, id)
	if len(second.PublicLinks) != 1 {
		t.Fatalf("second payload = %+v", second.PublicLinks)
	}
	if second.PublicLinks[0].URL != again.URL {
		t.Errorf("Share payload URL = %q, want the rotated %q", second.PublicLinks[0].URL, again.URL)
	}
	count, err := services.CountPublicSubmissionsByLink(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if count[wantID] != 1 {
		t.Errorf("submissions preserved = %d, want 1", count[wantID])
	}
}

// TestRevokedLinkHasNoCopyURL ensures a revoked link is never offered as a working capability.
func TestRevokedLinkHasNoCopyURL(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Revoked Work\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()

	privateURL, _ := mintLink(t, srv.URL, id)
	row := shareLinkPayload(t, srv.URL, id).Links[0]
	status, _, _ := postForm(t, srv.URL+"/share/link/revoke", url.Values{"id": {id}, "link": {row.ID}}, nil)
	if status != http.StatusOK {
		t.Fatalf("revoke = %d", status)
	}

	for _, link := range shareLinkPayload(t, srv.URL, id).Links {
		if link.URL != "" {
			t.Errorf("revoked link still has URL %q", link.URL)
		}
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), privateURL); status != http.StatusNotFound {
		t.Errorf("revoked URL = %d, want 404", status)
	}
}
