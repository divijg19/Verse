package tests

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/services"
)

// This file covers publication.
//
// Publishing is the boundary between the author's private library and the reader's public site, and
// until v0.5.0 the only way across it was hand-written SQL. The tests are ordered by what a mistake
// costs: the ones that would put the wrong work in front of a reader come first.
//
// The two failures are told apart on purpose. A work that does not exist and a work in the recycle
// bin are different problems, and the second one has a different remedy.

// publishedPoemContents lists the works the publisher can see.
func publishedPoemContents(t *testing.T) []string {
	t.Helper()

	poems, err := services.ListPublishedPoems(context.Background())
	if err != nil {
		t.Fatalf("list published poems: %v", err)
	}

	out := make([]string, 0, len(poems))
	for _, p := range poems {
		out = append(out, p.Content)
	}
	return out
}

// TestPublishingMakesAWorkVisibleToThePublisher is the boundary itself.
func TestPublishingMakesAWorkVisibleToThePublisher(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "ready for readers")

	if got := publishedPoemContents(t); len(got) != 0 {
		t.Fatalf("a draft is already visible to the publisher: %v", got)
	}

	if err := services.PublishPoem(context.Background(), id); err != nil {
		t.Fatalf("publish: %v", err)
	}

	got := publishedPoemContents(t)
	if len(got) != 1 || got[0] != "ready for readers" {
		t.Fatalf("the publisher shows %v, want the published work", got)
	}
}

// TestUnpublishingTakesAWorkBackOffTheSite is the other half of the boundary.
//
// Unpublishing clears the instant rather than deleting the work, so it is undoable and the history
// is untouched.
func TestUnpublishingTakesAWorkBackOffTheSite(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "on the site for now")
	if err := services.PublishPoem(context.Background(), id); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if err := services.UnpublishPoem(context.Background(), id); err != nil {
		t.Fatalf("unpublish: %v", err)
	}

	if got := publishedPoemContents(t); len(got) != 0 {
		t.Fatalf("the publisher still shows %v", got)
	}
	if got := poemContentByID(t, id); got != "on the site for now" {
		t.Fatalf("the work's text changed when it was unpublished: %q", got)
	}
}

// TestUnpublishingAWorkThatWasNeverPublishedReportsNotFound keeps a repeated withdrawal a clean miss.
//
// A silent success would tell the author they had taken something off the site that was never on
// it, which is the opposite of what happened.
func TestUnpublishingAWorkThatWasNeverPublishedReportsNotFound(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "never published")

	if err := services.UnpublishPoem(context.Background(), id); !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("unpublishing a draft = %v, want ErrNotFound", err)
	}
}

// TestADeletedWorkCannotBePublished covers the state the author cannot reach.
//
// The publisher's filter is published_at IS NOT NULL AND deleted_at IS NULL, so a work that is both
// published and deleted is a row the site would never show and the author would never be able to
// undo. The refusal is here so the state is never created.
func TestADeletedWorkCannotBePublished(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "in the recycle bin")
	if err := services.SoftDeletePoem(context.Background(), id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	err := services.PublishPoem(context.Background(), id)
	if !errors.Is(err, services.ErrNotPublishable) {
		t.Fatalf("publishing a deleted work = %v, want ErrNotPublishable", err)
	}

	// The state really is absent, not merely refused.
	if got := publishedPoemContents(t); len(got) != 0 {
		t.Fatalf("a deleted work is visible to the publisher: %v", got)
	}
}

// TestPublishingAnUnknownWorkReportsNotFound covers a mistyped or stale id.
func TestPublishingAnUnknownWorkReportsNotFound(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	if err := services.PublishPoem(context.Background(), "11111111-2222-3333-4444-555555555555"); !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("publishing an unknown work = %v, want ErrNotFound", err)
	}
}

// TestThePublicationStateIsReadable covers the read the surfaces use.
//
// The live reads filter on published_at being null, so they cannot report it. This is the read that
// does, and it is what the work's own screen shows the author.
func TestThePublicationStateIsReadable(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "a work with a state")

	state, err := services.PublicationState(context.Background(), id)
	if err != nil {
		t.Fatalf("read the state: %v", err)
	}
	if state.Published {
		t.Error("a fresh work reports itself as published")
	}
	if state.PublishedAt != nil {
		t.Errorf("a draft carries a publication instant: %v", state.PublishedAt)
	}

	if err := services.PublishPoem(context.Background(), id); err != nil {
		t.Fatalf("publish: %v", err)
	}

	state, err = services.PublicationState(context.Background(), id)
	if err != nil {
		t.Fatalf("read the state: %v", err)
	}
	if !state.Published {
		t.Error("a published work reports itself as a draft")
	}
	if state.PublishedAt == nil {
		t.Error("a published work carries no publication instant")
	}
}

// TestThePublishRouteRejectsAMalformedFlag covers the form's contract.
//
// The flag is "1" or "0". Anything else is a caller that does not know what it is asking for, and
// guessing on its behalf would publish a work it may have meant to withdraw.
func TestThePublishRouteRejectsAMalformedFlag(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	defer srv.Close()

	id := insertPoem(t, "a work with a careful author")

	status, _, _ := postForm(t, srv.URL+"/poem/publish", url.Values{
		"id":      {id},
		"publish": {"maybe"},
	}, nil)

	if status != http.StatusBadRequest {
		t.Fatalf("POST /poem/publish with a malformed flag = %d, want 400", status)
	}

	if got := publishedPoemContents(t); len(got) != 0 {
		t.Fatalf("a malformed flag published %v", got)
	}
}

// TestTheWorkScreenShowsThePublicationState covers the surface.
//
// The author has to be able to see whether a work is on the public site without going to the
// database, because the answer is the difference between a reader seeing a work and not.
func TestTheWorkScreenShowsThePublicationState(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	defer srv.Close()

	id := insertPoem(t, "a work the author can see")

	status, body, _ := get(t, srv.URL+"/poem/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /poem/%s = %d, want 200", id, status)
	}
	if !strings.Contains(body, "Draft") {
		t.Errorf("the work's screen does not say the work is a draft")
	}

	status, body, _ = postForm(t, srv.URL+"/poem/publish", url.Values{
		"id":      {id},
		"publish": {"1"},
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("POST /poem/publish = %d, want 200", status)
	}
	if !strings.Contains(body, "Published") {
		t.Errorf("the work's screen does not say the work is published")
	}
}
