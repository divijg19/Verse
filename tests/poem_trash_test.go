package tests

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/services"
)

// This file covers the recycle. deleted_at used to be a one-way trip: the row survived in Postgres,
// so a deleted work was technically recoverable by hand in SQL, but the application offered no
// listing, no restore and no purge. The author had no way back to a poem they deleted by accident.
//
// The tests are ordered by what a mistake costs. TestRestoreReturnsADeletedPoemToTheLibrary is the
// one that matters; the rest pin the properties that make trusting it safe, in particular that
// nothing deleted leaks back into the surfaces the author reads.

// TestRestoreReturnsADeletedPoemToTheLibrary is the invariant the feature exists for.
func TestRestoreReturnsADeletedPoemToTheLibrary(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "deleted by accident")
	if err := services.SoftDeletePoem(context.Background(), id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if err := services.RestorePoem(context.Background(), id); err != nil {
		t.Fatalf("restore: %v", err)
	}

	poem, err := services.GetPoem(context.Background(), id)
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}
	if poem.Content != "deleted by accident" {
		t.Fatalf("content after restore = %q, want the original", poem.Content)
	}
	if got := listActivePoemContents(t); len(got) != 1 || got[0] != "deleted by accident" {
		t.Fatalf("the library shows %v, want the restored work", got)
	}
}

// TestRestoreOfAnActivePoemReportsNotFound keeps a repeated restore a clean miss.
//
// This mirrors SoftDeletePoem, which reports an already-deleted row as not found rather than
// succeeding silently. A restore that reported success for an active poem would be a false
// confirmation that something had been recovered when nothing had.
func TestRestoreOfAnActivePoemReportsNotFound(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "never deleted")

	if err := services.RestorePoem(context.Background(), id); err == nil {
		t.Fatal("restoring an active poem reported success")
	}
	if got := poemContentByID(t, id); got != "never deleted" {
		t.Fatalf("content = %q, want it untouched", got)
	}
}

// TestRestoreOfAnUnknownPoemReportsNotFound covers a mistyped or stale id.
func TestRestoreOfAnUnknownPoemReportsNotFound(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	if err := services.RestorePoem(context.Background(),
		"00000000-0000-0000-0000-000000000000"); err == nil {
		t.Fatal("restoring an unknown poem reported success")
	}
}

// TestDeletedPoemsDoNotLeakIntoTheLibrary is the property that makes the recycle trustworthy.
//
// Every read that feeds the library already filtered on deleted_at IS NULL. If any of them stopped
// doing so, a deleted work would reappear in the surface the author reads, which is the specific
// surprise the recycle is meant to prevent.
func TestDeletedPoemsDoNotLeakIntoTheLibrary(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	kept := insertPoem(t, "a kept work")
	gone := insertPoem(t, "a deleted work")

	if err := services.SoftDeletePoem(context.Background(), gone); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	poems, err := services.ListPoems(context.Background(), 100, 0)
	if err != nil {
		t.Fatalf("list poems: %v", err)
	}
	for _, p := range poems {
		if p.ID == gone {
			t.Fatal("a deleted work appeared in the library listing")
		}
	}
	if len(poems) != 1 || poems[0].ID != kept {
		t.Fatalf("the library lists %d poem(s), want only the kept one", len(poems))
	}

	// Search, which is a separate query with its own filter.
	found, err := services.SearchPoems(context.Background(), "deleted work", 100, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("search returned %d result(s) for a deleted work", len(found))
	}

	// And the direct read.
	if _, err := services.GetPoem(context.Background(), gone); err == nil {
		t.Fatal("a deleted work was readable as though it were active")
	}
}

// TestTheRecycleListsOnlyDeletedWorks, most recently deleted first.
func TestTheRecycleListsOnlyDeletedWorks(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	kept := insertPoem(t, "still here")
	first := insertPoem(t, "deleted first")
	second := insertPoem(t, "deleted second")

	if err := services.SoftDeletePoem(context.Background(), first); err != nil {
		t.Fatalf("delete first: %v", err)
	}
	if err := services.SoftDeletePoem(context.Background(), second); err != nil {
		t.Fatalf("delete second: %v", err)
	}

	trash, err := services.ListDeletedPoems(context.Background(), 100, 0)
	if err != nil {
		t.Fatalf("list deleted: %v", err)
	}
	if len(trash) != 2 {
		t.Fatalf("the recycle lists %d poem(s), want 2", len(trash))
	}
	// Newest deletion first: the question the screen answers is "what did I just lose".
	if trash[0].ID != second {
		t.Fatalf("the recycle is not ordered by deletion time; first entry is %q, want %q",
			trash[0].ID, second)
	}
	for _, p := range trash {
		if p.ID == kept {
			t.Fatal("an active work appeared in the recycle")
		}
	}
}

// TestAnEmptyRecycleIsNotAnError: a fresh install has nothing deleted, and the screen must say so
// rather than failing.
func TestAnEmptyRecycleIsNotAnError(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	trash, err := services.ListDeletedPoems(context.Background(), 100, 0)
	if err != nil {
		t.Fatalf("list deleted on an empty library: %v", err)
	}
	if len(trash) != 0 {
		t.Fatalf("the recycle lists %d poem(s) on an empty library", len(trash))
	}
}

// TestARestoredPoemKeepsItsHistory, so undeleting does not cost the work its undo trail.
func TestARestoredPoemKeepsItsHistory(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "first draft")
	if err := services.UpdatePoem(context.Background(), id, "second draft"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := services.SoftDeletePoem(context.Background(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := services.RestorePoem(context.Background(), id); err != nil {
		t.Fatalf("restore: %v", err)
	}

	versions, err := services.ListPoemVersions(context.Background(), id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 1 || versions[0].Content != "first draft" {
		t.Fatalf("history after undelete = %v, want the first draft retained", contentsOf(versions))
	}
}

// TestRecycleRoutesExerciseTheFeature covers the HTTP surface, not just the service layer.
func TestRecycleRoutesExerciseTheFeature(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "an accidental deletion")
	if err := services.SoftDeletePoem(context.Background(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	srv := newTestServer(t)

	status, body, _ := get(t, srv.URL+"/recycle", nil)
	if status != 200 {
		t.Fatalf("GET /recycle = %d, want 200; body: %s", status, truncate([]byte(body)))
	}
	if !strings.Contains(body, "an accidental deletion") {
		t.Error("the recycle does not list the deleted work")
	}

	status, body, _ = postForm(t, srv.URL+"/poem/undelete", url.Values{"id": {id}}, nil)
	if status != 200 {
		t.Fatalf("POST /poem/undelete = %d, want 200; body: %s", status, truncate([]byte(body)))
	}
	if got := poemContentByID(t, id); got != "an accidental deletion" {
		t.Fatalf("content after undeleting over HTTP = %q, want the original", got)
	}

	// And it must be gone from the recycle afterwards.
	_, body, _ = get(t, srv.URL+"/recycle", nil)
	if strings.Contains(body, "an accidental deletion") {
		t.Error("a restored work is still listed in the recycle")
	}
}

// TestUndeleteRequiresASession keeps the recycle as private as the library.
func TestUndeleteRequiresASession(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "private work")
	if err := services.SoftDeletePoem(context.Background(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	srv := newLoginServer(t, true)
	c := newAnonymousClient(t, srv)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp := doGet(t, c, srv.URL+"/recycle")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /recycle without a session = %d, want 303 to the login page", resp.StatusCode)
	}
}

// listActivePoemContents returns the content of every poem the library considers active.
func listActivePoemContents(t *testing.T) []string {
	t.Helper()
	poems, err := services.ListPoems(context.Background(), 100, 0)
	if err != nil {
		t.Fatalf("list poems: %v", err)
	}
	out := make([]string, 0, len(poems))
	for _, p := range poems {
		out = append(out, p.Content)
	}
	return out
}
