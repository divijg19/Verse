package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/services"
)

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type mintResult struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expiresAt"`
	Created   bool   `json:"created"`
}

func mintPrivateResult(t *testing.T, srvURL, poemID string) mintResult {
	t.Helper()
	status, body, _ := postForm(t, srvURL+"/share/link", urlValues("id", poemID), nil)
	if status != http.StatusOK {
		t.Fatalf("POST /share/link = %d; body: %s", status, truncate([]byte(body)))
	}
	var out mintResult
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestPrivateIdempotentReturnAndExplicitNew pins the two private actions: the primary button reuses
// the active link without changing its token or expiry, and New adds a second conversation.
func TestPrivateIdempotentReturnAndExplicitNew(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "An Idempotent Work\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()

	first := mintPrivateResult(t, srv.URL, id)
	if !first.Created {
		t.Fatal("first private mint reported created=false")
	}
	second := mintPrivateResult(t, srv.URL, id)
	// The wire representation must match too: a scanned timestamptz formatted in local time would
	// denote the same instant but differ from the UTC string the mint path returns.
	if second.ExpiresAt != first.ExpiresAt {
		t.Errorf("expiry changed on reuse: %q vs %q", first.ExpiresAt, second.ExpiresAt)
	}
	if second.Created || second.ID != first.ID || second.URL != first.URL {
		t.Errorf("second private mint = %+v, want the first unchanged with created=false", second)
	}
	links, err := services.ListPrivateLinks(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("idempotent mint added rows: %d", len(links))
	}

	// New always mints a distinct capability and leaves the first one working.
	status, body, _ := postForm(t, srv.URL+"/share/link/new", urlValues("id", id), nil)
	if status != http.StatusOK {
		t.Fatalf("New = %d; body: %s", status, truncate([]byte(body)))
	}
	var fresh mintResult
	if err := json.Unmarshal([]byte(body), &fresh); err != nil {
		t.Fatal(err)
	}
	if !fresh.Created || fresh.ID == first.ID || fresh.URL == first.URL {
		t.Errorf("New = %+v, want a fresh link with created=true", fresh)
	}
	links, err = services.ListPrivateLinks(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("New left %d rows, want 2", len(links))
	}
	guest := guestClient(t, srv)
	if status, _, _ := guestGet(t, guest, first.URL); status != http.StatusSeeOther {
		t.Errorf("New invalidated the first link: %d", status)
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), fresh.URL); status != http.StatusSeeOther {
		t.Errorf("new link does not resolve: %d", status)
	}
}

// TestConcurrentIdempotentMints exercises the locks at the service layer. Under READ COMMITTED, a
// simultaneous first click must produce one link and exactly one created=true result for each kind.
func TestConcurrentIdempotentMints(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	privateID := insertPoem(t, "Concurrent Private\n\nIts body.")
	publicID := insertPoem(t, "Concurrent Public\n\nIts body.")
	if err := services.PublishPoem(t.Context(), publicID); err != nil {
		t.Fatal(err)
	}
	key := testLinkKey()

	type outcome struct {
		id      string
		created bool
		err     error
	}
	collect := func(results <-chan outcome, label string) int {
		ids := map[string]int{}
		creations := 0
		for result := range results {
			if result.err != nil {
				t.Fatalf("concurrent %s mint: %v", label, result.err)
			}
			ids[result.id]++
			if result.created {
				creations++
			}
		}
		if len(ids) != 1 || creations != 1 {
			t.Errorf("concurrent %s mint: %d ids, %d created flags; want 1 and 1 (%v)", label, len(ids), creations, ids)
		}
		return creations
	}

	const workers = 8
	privateResults := make(chan outcome, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, id, _, created, err := services.GetOrCreatePrivateLink(t.Context(), key, privateID)
			privateResults <- outcome{id: id, created: created, err: err}
		}()
	}
	wg.Wait()
	close(privateResults)
	collect(privateResults, "private")

	publicResults := make(chan outcome, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, id, created, err := services.GetOrCreatePublicLink(t.Context(), key, publicID)
			publicResults <- outcome{id: id, created: created, err: err}
		}()
	}
	wg.Wait()
	close(publicResults)
	collect(publicResults, "public")

	links, err := services.ListPublicLinks(t.Context(), publicID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Errorf("concurrent public mint left %d rows, want 1", len(links))
	}
}

// TestCreatedFlagComesFromTheWinningInsert is the HTTP-level companion to the concurrency test:
// under simultaneous clicks only one response may claim it created the link.
func TestCreatedFlagComesFromTheWinningInsert(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "One Winner\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()

	const workers = 6
	csrf := csrfFromJar(t, srv.URL)
	results := make(chan mintResult, workers)
	errs := make(chan error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			values := url.Values{"id": {id}, "csrf": {csrf}}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/share/link", strings.NewReader(values.Encode()))
			if err != nil {
				errs <- err
				return
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := authClient.Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- errors.New("status " + http.StatusText(resp.StatusCode))
				return
			}
			var out mintResult
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				errs <- err
				return
			}
			results <- out
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	created, ids := 0, map[string]bool{}
	for out := range results {
		if out.Created {
			created++
		}
		ids[out.ID] = true
	}
	if created != 1 || len(ids) != 1 {
		t.Errorf("concurrent clicks: %d created flags, %d distinct ids; want 1 and 1", created, len(ids))
	}
}

// TestRevokedPublicRequiresExplicitRotate pins the rule that an idempotent click never revives a
// withdrawn capability.
func TestRevokedPublicRequiresExplicitRotate(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "Revoked Public\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.Close()
	oldToken := mintPublicLink(t, srv.URL, id)
	linkID := shareLinkPayload(t, srv.URL, id).PublicLinks[0].ID

	status, _, _ := postForm(t, srv.URL+"/share/public-link/revoke", url.Values{"id": {id}, "link": {linkID}}, nil)
	if status != http.StatusOK {
		t.Fatalf("revoke = %d", status)
	}
	status, body, _ := postForm(t, srv.URL+"/share/public-link", urlValues("id", id), nil)
	if status != http.StatusConflict {
		t.Fatalf("idempotent click on revoked link = %d, want 409; body: %s", status, truncate([]byte(body)))
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+oldToken); status != http.StatusNotFound {
		t.Errorf("revoked link was revived: %d", status)
	}
	ended := shareLinkPayload(t, srv.URL, id).PublicLinks[0]
	if ended.State != "revoked" || ended.URL != "" || ended.Copyable {
		t.Errorf("revoked public row = %+v, want revoked with no URL", ended)
	}

	status, body, _ = postForm(t, srv.URL+"/share/public-link/rotate", url.Values{"id": {id}, "link": {linkID}}, nil)
	if status != http.StatusOK {
		t.Fatalf("rotate = %d; body: %s", status, truncate([]byte(body)))
	}
	var rotated mintResult
	if err := json.Unmarshal([]byte(body), &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.ID != linkID || !rotated.Created {
		t.Errorf("rotate = %+v, want same id and created=true", rotated)
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), rotated.URL); status != http.StatusOK {
		t.Errorf("rotated link does not resolve: %d", status)
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+oldToken); status != http.StatusNotFound {
		t.Errorf("old token resolves after rotate: %d", status)
	}
}

// TestLegacyPublicLinkRequiresExplicitRotate covers the pre-v0.5.9 row with no seed: an idempotent
// click must not silently change a capability already in someone's hands.
func TestLegacyPublicLinkRequiresExplicitRotate(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "Legacy Public\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	legacyToken := strings.Repeat("a", 64)
	hash := sha256Hex(legacyToken)
	linkID := uuid.NewString()
	if _, err := database.Pool.Exec(t.Context(), `
        INSERT INTO public_links (id, poem_id, token_hash, created_at)
        VALUES ($1, $2, $3, $4)`, linkID, id, hash, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t)
	defer srv.Close()
	status, body, _ := postForm(t, srv.URL+"/share/public-link", urlValues("id", id), nil)
	if status != http.StatusConflict {
		t.Fatalf("idempotent click on legacy link = %d, want 409; body: %s", status, truncate([]byte(body)))
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+legacyToken); status != http.StatusOK {
		t.Errorf("legacy capability was invalidated by a plain click: %d", status)
	}

	status, body, _ = postForm(t, srv.URL+"/share/public-link/rotate", url.Values{"id": {id}, "link": {linkID}}, nil)
	if status != http.StatusOK {
		t.Fatalf("rotate legacy = %d; body: %s", status, truncate([]byte(body)))
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+legacyToken); status != http.StatusNotFound {
		t.Errorf("old legacy token resolves after rotate: %d", status)
	}
}

// TestUnpublishedPublicActionsAreRefused ensures the published gate is applied on reuse and rotate,
// not only on the original mint write.
func TestUnpublishedPublicActionsAreRefused(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Draft\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()

	status, _, _ := postForm(t, srv.URL+"/share/public-link", urlValues("id", id), nil)
	if status != http.StatusConflict {
		t.Errorf("POST /share/public-link on a draft = %d, want 409", status)
	}
	status, _, _ = postForm(t, srv.URL+"/share/public-link/rotate", url.Values{"id": {id}, "link": {uuid.NewString()}}, nil)
	if status != http.StatusConflict {
		t.Errorf("POST /share/public-link/rotate on a draft = %d, want 409", status)
	}
}

// TestOriginOverrideOnIdempotentHits verifies that reusing a link still honors a configured author
// origin rather than the request's Host.
func TestOriginOverrideOnIdempotentHits(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "Origin Override\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()
	t.Setenv("VERSE_AUTHOR_BASE_URL", "https://author.example/")

	first := mintPrivateResult(t, srv.URL, id)
	if !strings.HasPrefix(first.URL, "https://author.example/s/") {
		t.Fatalf("first URL = %q, want the configured author origin", first.URL)
	}
	second := mintPrivateResult(t, srv.URL, id)
	if second.URL != first.URL {
		t.Errorf("idempotent hit rebuilt the URL differently: %q vs %q", second.URL, first.URL)
	}
}
