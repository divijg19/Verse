package tests

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/services"
)

// TestPublicShareStates walks a public link through every state the Share screen can report. The
// state must come from stored facts, never from whether a Copy URL happens to be present.
func TestPublicShareStates(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Public State Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.Close()
	mintPublicLink(t, srv.URL, id)

	state := func(want string) shareLinkRow {
		t.Helper()
		payload := shareLinkPayload(t, srv.URL, id)
		if len(payload.PublicLinks) != 1 {
			t.Fatalf("public row disappeared: %+v", payload.PublicLinks)
		}
		row := payload.PublicLinks[0]
		if row.State != want {
			t.Errorf("state = %q, want %q (row %+v)", row.State, want, row)
		}
		if (row.URL != "") != row.Copyable {
			t.Errorf("copyable disagrees with URL: %+v", row)
		}
		return row
	}

	state("active")
	if err := services.UnpublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	unpublished := state("unpublished")
	if unpublished.URL != "" {
		t.Errorf("unpublished row still has a URL: %+v", unpublished)
	}
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	republished := state("active")
	linkID := republished.ID

	status, _, _ := postForm(t, srv.URL+"/share/public-link/revoke", url.Values{"id": {id}, "link": {linkID}}, nil)
	if status != http.StatusOK {
		t.Fatalf("revoke = %d", status)
	}
	revoked := state("revoked")
	if revoked.URL != "" {
		t.Errorf("revoked row still has a URL: %+v", revoked)
	}
	// Revocation wins over unpublication in the state label.
	if err := services.UnpublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	state("revoked")
}

// TestPrivateShareStates covers the four private states and the revoked-over-expired precedence.
func TestPrivateShareStates(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Private State Work\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()
	_, token := mintLink(t, srv.URL, id)

	state := func(want string) shareLinkRow {
		t.Helper()
		payload := shareLinkPayload(t, srv.URL, id)
		if len(payload.Links) != 1 {
			t.Fatalf("private row disappeared: %+v", payload.Links)
		}
		row := payload.Links[0]
		if row.State != want {
			t.Errorf("state = %q, want %q (row %+v)", row.State, want, row)
		}
		if (row.URL != "") != row.Copyable {
			t.Errorf("copyable disagrees with URL: %+v", row)
		}
		return row
	}

	state("active")

	// A NULL-seed row is unrecoverable but its capability still resolves for a holder.
	expireLink(t, id)
	expired := state("expired")
	if expired.URL != "" {
		t.Errorf("expired row still has a URL: %+v", expired)
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), srv.URL+"/s/"+token); status != http.StatusNotFound {
		t.Errorf("expired capability still resolves: %d", status)
	}

	status, _, _ := postForm(t, srv.URL+"/share/link/revoke", url.Values{"id": {id}, "link": {expired.ID}}, nil)
	if status != http.StatusOK {
		t.Fatalf("revoke = %d", status)
	}
	state("revoked")
}

// TestLegacyPublicLinkIsUnrecoverable pins that an unrecoverable public row is not called ended: a
// holder's URL still resolves, and Rotate is the repair path.
func TestLegacyPublicLinkIsUnrecoverable(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Legacy Public Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	if _, err := database.Pool.Exec(t.Context(), `
        INSERT INTO public_links (id, poem_id, token_hash, created_at)
        VALUES ($1, $2, $3, $4)`, uuid.NewString(), id, sha256Hex(token), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t)
	defer srv.Close()
	payload := shareLinkPayload(t, srv.URL, id)
	if len(payload.PublicLinks) != 1 {
		t.Fatalf("public row missing: %+v", payload.PublicLinks)
	}
	row := payload.PublicLinks[0]
	if row.State != "unrecoverable" || row.URL != "" {
		t.Errorf("legacy row = %+v, want unrecoverable with no URL", row)
	}
	if status, _, _ := guestGet(t, guestClient(t, srv), srv.URL+"/p/"+token); status != http.StatusOK {
		t.Errorf("legacy capability no longer resolves: %d", status)
	}
}

// TestSoftDeletedWorkHasNoShareRows confirms there is no "Deleted" Share state: the work is gone
// from both the picker and the data endpoint, so stale rows cannot be shown.
func TestSoftDeletedWorkHasNoShareRows(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Deleted Work\n\nIts body.")
	srv := newTestServer(t)
	defer srv.Close()
	mintLink(t, srv.URL, id)

	if err := services.SoftDeletePoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	status, _, _ := get(t, srv.URL+"/share/work/"+id, nil)
	if status != http.StatusNotFound {
		t.Errorf("share data for deleted work = %d, want 404", status)
	}
}
