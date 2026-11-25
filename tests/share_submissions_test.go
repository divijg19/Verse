package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/services"
)

type shareSubmission struct {
	ID        string    `json:"id"`
	LinkID    string    `json:"linkID"`
	GuestName string    `json:"guestName"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

func shareSubmissionsPayload(t *testing.T, srvURL, poemID string) []shareSubmission {
	t.Helper()
	status, body, _ := get(t, srvURL+"/share/work/"+poemID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /share/work/%s = %d; body: %s", poemID, status, truncate([]byte(body)))
	}
	var data struct {
		PublicSubmissions []shareSubmission `json:"publicSubmissions"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatal(err)
	}
	return data.PublicSubmissions
}

func submitPublicSuggestion(t *testing.T, srv *httptest.Server, visitor *http.Client, token, name, content string) {
	t.Helper()
	status, page, _ := guestGet(t, visitor, srv.URL+"/p/"+token)
	if status != http.StatusOK {
		t.Fatalf("public page = %d", status)
	}
	status, body, _ := guestPost(t, visitor, srv.URL+"/p/"+token+"/submit", url.Values{
		"csrf": {csrfFromBody(t, page)}, "name": {name}, "content": {content},
	})
	if status != http.StatusOK {
		t.Fatalf("submit = %d; body: %s", status, truncate([]byte(body)))
	}
}

// TestShareSubmissionsAreScopedToTheSelectedWork proves the overlay's data cannot show another
// work's public suggestion: each work has one sentinel, and each payload contains only its own.
func TestShareSubmissionsAreScopedToTheSelectedWork(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	workA := insertPoem(t, "Work A\n\nBody of A.")
	workB := insertPoem(t, "Work B\n\nBody of B.")
	for _, id := range []string{workA, workB} {
		if err := services.PublishPoem(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	srv := newTestServer(t)
	defer srv.Close()
	tokenA := mintPublicLink(t, srv.URL, workA)
	tokenB := mintPublicLink(t, srv.URL, workB)
	visitor := guestClient(t, srv)

	submitPublicSuggestion(t, srv, visitor, tokenA, "Reader A", "ALPHA SENTINEL")
	submitPublicSuggestion(t, srv, visitor, tokenB, "Reader B", "BETA SENTINEL")

	submissions := shareSubmissionsPayload(t, srv.URL, workA)
	if len(submissions) != 1 {
		t.Fatalf("got %d submissions for work A, want 1: %+v", len(submissions), submissions)
	}
	if submissions[0].Content != "ALPHA SENTINEL" {
		t.Errorf("content = %q, want ALPHA SENTINEL", submissions[0].Content)
	}
	if submissions[0].GuestName != "Reader A" {
		t.Errorf("guest name = %q, want Reader A", submissions[0].GuestName)
	}
	if submissions[0].LinkID == "" {
		t.Error("the submission carries no link attribution")
	}
	if submissions[0].CreatedAt.IsZero() {
		t.Error("the submission carries no timestamp")
	}
	if strings.Contains(submissions[0].Content, "BETA SENTINEL") {
		t.Error("work A's payload leaked work B's suggestion")
	}
	other := shareSubmissionsPayload(t, srv.URL, workB)
	if len(other) != 1 || other[0].Content != "BETA SENTINEL" ||
		strings.Contains(other[0].Content, "ALPHA SENTINEL") {
		t.Errorf("work B's payload = %+v, want only its own suggestion", other)
	}
}

// TestShareSubmissionsOrderEmptyAndRevoked pins the overlay's remaining data contracts: newest
// first, an empty array rather than null, and retained text after the link is revoked.
func TestShareSubmissionsOrderEmptyAndRevoked(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	work := insertPoem(t, "A Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), work); err != nil {
		t.Fatal(err)
	}
	empty := insertPoem(t, "An Empty Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), empty); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.Close()
	visitor := guestClient(t, srv)

	status, body, _ := get(t, srv.URL+"/share/work/"+empty, nil)
	if status != http.StatusOK {
		t.Fatalf("GET empty share = %d", status)
	}
	if !strings.Contains(body, `"publicSubmissions":[]`) {
		t.Errorf("an empty overlay payload is not an empty array: %s", truncate([]byte(body)))
	}

	token := mintPublicLink(t, srv.URL, work)
	submitPublicSuggestion(t, srv, visitor, token, "Reader", "FIRST")
	submitPublicSuggestion(t, srv, visitor, token, "Reader", "SECOND")

	submissions := shareSubmissionsPayload(t, srv.URL, work)
	if len(submissions) != 2 || submissions[0].Content != "SECOND" || submissions[1].Content != "FIRST" {
		t.Fatalf("submissions are not newest first: %+v", submissions)
	}

	links, err := services.ListPublicLinks(t.Context(), work)
	if err != nil || len(links) != 1 {
		t.Fatalf("links = %+v, err = %v", links, err)
	}
	if err := services.RevokePublicLink(t.Context(), work, links[0].ID); err != nil {
		t.Fatal(err)
	}
	after := shareSubmissionsPayload(t, srv.URL, work)
	if len(after) != 2 {
		t.Errorf("revocation removed retained submissions: %+v", after)
	}
}

// TestListRecentPublicSubmissionsRespectsTheBound pins the LIMIT in the Share read: the overlay is
// bounded, newest first, while History keeps the complete record.
func TestListRecentPublicSubmissionsRespectsTheBound(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Bounded Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.Close()
	token := mintPublicLink(t, srv.URL, id)
	visitor := guestClient(t, srv)
	for i := 0; i < 3; i++ {
		submitPublicSuggestion(t, srv, visitor, token, "Reader", fmt.Sprintf("SENTINEL-%d", i))
	}

	recent, err := services.ListRecentPublicSubmissions(t.Context(), id, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].Content != "SENTINEL-2" || recent[1].Content != "SENTINEL-1" {
		t.Fatalf("recent = %+v, want the two newest in order", recent)
	}
	none, err := services.ListRecentPublicSubmissions(t.Context(), id, 0)
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("limit 0 = %+v, err = %v, want a non-nil empty slice", none, err)
	}
	all, err := services.ListPublicSubmissions(t.Context(), id)
	if err != nil || len(all) != 3 {
		t.Fatalf("unbounded list = %+v, err = %v, want all 3", all, err)
	}
}
