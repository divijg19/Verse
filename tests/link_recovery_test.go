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
	ID  string `json:"id"`
	URL string `json:"url"`
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

// TestPublicIdempotentReturnAndExplicitRotateKeepIdentity pins both halves of v0.5.10: a repeated
// Public Link click returns the existing capability without a write, while an explicit Rotate issues
// a fresh token on the same row. In both cases the submissions and their attribution survive.
func TestPublicIdempotentReturnAndExplicitRotateKeepIdentity(t *testing.T) {
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

	// Snapshot the row: an accidental write on the reuse path must be caught even if the response
	// still looks correct.
	var beforeHash, beforeCreated string
	if err := database.Pool.QueryRow(t.Context(), `
        SELECT token_hash, created_at::text FROM public_links WHERE id = $1`, wantID).
		Scan(&beforeHash, &beforeCreated); err != nil {
		t.Fatal(err)
	}

	// A repeated click is a pure read: the same id and URL, created:false, and the old token still live.
	status, body, _ := postForm(t, srv.URL+"/share/public-link", urlValues("id", id), nil)
	if status != http.StatusOK {
		t.Fatalf("second click with a submission = %d; body: %s", status, truncate([]byte(body)))
	}
	var reused struct {
		ID      string `json:"id"`
		URL     string `json:"url"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal([]byte(body), &reused); err != nil {
		t.Fatal(err)
	}
	if reused.ID != wantID || reused.URL != srv.URL+"/p/"+token || reused.Created {
		t.Errorf("idempotent click = %+v, want original id/url with created=false", reused)
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+token); status != http.StatusOK {
		t.Errorf("idempotent click invalidated the old token: %d", status)
	}
	var afterHash, afterCreated string
	if err := database.Pool.QueryRow(t.Context(), `
        SELECT token_hash, created_at::text FROM public_links WHERE id = $1`, wantID).
		Scan(&afterHash, &afterCreated); err != nil {
		t.Fatal(err)
	}
	if afterHash != beforeHash || afterCreated != beforeCreated {
		t.Errorf("idempotent click mutated the row: hash %q -> %q, created %q -> %q",
			beforeHash, afterHash, beforeCreated, afterCreated)
	}

	// The explicit Rotate changes the token but keeps the row id and its history.
	status, body, _ = postForm(t, srv.URL+"/share/public-link/rotate", url.Values{"id": {id}, "link": {wantID}}, nil)
	if status != http.StatusOK {
		t.Fatalf("rotate with a submission = %d; body: %s", status, truncate([]byte(body)))
	}
	var rotated struct {
		ID      string `json:"id"`
		URL     string `json:"url"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal([]byte(body), &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.ID != wantID || !rotated.Created || rotated.URL == srv.URL+"/p/"+token {
		t.Errorf("rotate = %+v, want same id, fresh URL, created=true", rotated)
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+token); status != http.StatusNotFound {
		t.Errorf("old public token still resolves after rotate: %d", status)
	}
	// The token_hash guard also rejects an in-flight POST that already has the old CSRF cookie.
	if status, _, _ = guestPost(t, guest, srv.URL+"/p/"+token+"/submit", url.Values{
		"csrf": {csrfFromBody(t, page)}, "name": {"Reader"}, "content": {"Too late."},
	}); status != http.StatusNotFound {
		t.Errorf("old token accepted a submission after rotate: %d", status)
	}

	second := shareLinkPayload(t, srv.URL, id)
	if len(second.PublicLinks) != 1 {
		t.Fatalf("second payload = %+v", second.PublicLinks)
	}
	if second.PublicLinks[0].URL != rotated.URL {
		t.Errorf("Share payload URL = %q, want the rotated %q", second.PublicLinks[0].URL, rotated.URL)
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

// TestSecretRotationIsFailClosedAcrossTheAuthoringFlow exercises the documented rotation contract
// end to end: holders keep their URLs, the browser loses Copy, the private primary mints a fresh
// row, the public primary refuses with 409, and explicit Rotate repairs the public row.
func TestSecretRotationIsFailClosedAcrossTheAuthoringFlow(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Rotating Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}

	first := newTestServerWithSecret(t, testAuthSecret)
	privateURL, _ := mintLink(t, first.URL, id)
	publicToken := mintPublicLink(t, first.URL, id)
	publicURL := first.URL + "/p/" + publicToken

	second := newTestServerWithSecret(t, testAuthSecret+"-after-rotation")

	payload := shareLinkPayload(t, second.URL, id)
	if len(payload.Links) == 0 || len(payload.PublicLinks) == 0 {
		t.Fatalf("Share lost the links after rotation: %+v", payload)
	}
	if payload.Links[0].URL != "" || payload.PublicLinks[0].URL != "" {
		t.Errorf("a stale URL was emitted after rotation: %+v", payload)
	}

	// Resolution is hash-based and key-independent, so old URLs keep working.
	guest := guestClient(t, second)
	if status, _, _ := guestGet(t, guest, privateURL); status != http.StatusSeeOther {
		t.Errorf("old private URL stopped working: %d", status)
	}
	if status, _, _ := guestGet(t, guestClient(t, second), publicURL); status != http.StatusOK {
		t.Errorf("old public URL stopped working: %d", status)
	}

	// The private primary is additive: it mints a fresh row rather than rewriting the old one.
	status, body, _ := postForm(t, second.URL+"/share/link", urlValues("id", id), nil)
	if status != http.StatusOK {
		t.Fatalf("private primary after rotation = %d; body: %s", status, truncate([]byte(body)))
	}
	var fresh mintResult
	if err := json.Unmarshal([]byte(body), &fresh); err != nil {
		t.Fatal(err)
	}
	if !fresh.Created || fresh.URL == privateURL {
		t.Errorf("private primary did not mint a fresh link: %+v", fresh)
	}

	// The public primary cannot repair in place without invalidating holders, so it refuses.
	status, body, _ = postForm(t, second.URL+"/share/public-link", urlValues("id", id), nil)
	if status != http.StatusConflict {
		t.Fatalf("public primary after rotation = %d, want 409; body: %s", status, truncate([]byte(body)))
	}

	// Explicit Rotate is the repair path.
	publicID := payload.PublicLinks[0].ID
	status, body, _ = postForm(t, second.URL+"/share/public-link/rotate", url.Values{"id": {id}, "link": {publicID}}, nil)
	if status != http.StatusOK {
		t.Fatalf("public rotate after rotation = %d; body: %s", status, truncate([]byte(body)))
	}
	var rotated mintResult
	if err := json.Unmarshal([]byte(body), &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.ID != publicID || rotated.URL == publicURL {
		t.Errorf("rotate did not preserve the id and retire the old token: %+v", rotated)
	}
	if status, _, _ := guestGet(t, guestClient(t, second), publicURL); status != http.StatusNotFound {
		t.Errorf("old public token still resolves after rotate: %d", status)
	}
}
