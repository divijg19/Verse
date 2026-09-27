package tests

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/models"
	"github.com/divijg19/Verse/internal/services"
)

// This file covers version history, which exists because a saved work could previously be destroyed
// by a single bad edit: UpdatePoem overwrote content in place and retained nothing.
//
// The tests are ordered by what would be lost. TestUpdateRetainsTheReplacedText is the one that
// matters -- if it fails, work is being destroyed. The rest pin the properties that make the
// retained copy trustworthy: that it is a faithful record, that it cannot be edited after the fact,
// that restoring is itself reversible, and that a failure cannot leave a history that lies.

// TestUpdateRetainsTheReplacedText is the invariant the feature exists for.
func TestUpdateRetainsTheReplacedText(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "the first draft")

	if err := services.UpdatePoem(context.Background(), id, "the second draft"); err != nil {
		t.Fatalf("update: %v", err)
	}

	if got := poemContentByID(t, id); got != "the second draft" {
		t.Fatalf("content after update = %q, want the second draft", got)
	}

	versions, err := services.ListPoemVersions(context.Background(), id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("version count = %d, want 1", len(versions))
	}
	if versions[0].Content != "the first draft" {
		t.Fatalf("the retained version is %q, want the text the update replaced", versions[0].Content)
	}
	if versions[0].PoemID != id {
		t.Fatalf("version points at poem %q, want %q", versions[0].PoemID, id)
	}
}

// TestRepeatedUpdatesRetainEveryIntermediateDraft walks the whole sequence, because retaining only
// the most recent prior text would satisfy the single-update case while still losing work.
func TestRepeatedUpdatesRetainEveryIntermediateDraft(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "draft one")

	drafts := []string{"draft two", "draft three", "draft four"}
	for _, d := range drafts {
		if err := services.UpdatePoem(context.Background(), id, d); err != nil {
			t.Fatalf("update to %q: %v", d, err)
		}
	}

	versions, err := services.ListPoemVersions(context.Background(), id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	// Three updates, each replacing a distinct text, so three superseded revisions.
	if len(versions) != 3 {
		t.Fatalf("version count = %d, want 3; every superseded draft must be retained", len(versions))
	}

	want := []string{"draft three", "draft two", "draft one"} // newest first
	for i, w := range want {
		if versions[i].Content != w {
			t.Errorf("version %d = %q, want %q (history must be newest first)", i, versions[i].Content, w)
		}
	}
}

// TestNoOpUpdateRecordsNothing keeps a history readable. The previous text equals the new one, so
// nothing is at stake, and one entry per autosave would make the list useless.
func TestNoOpUpdateRecordsNothing(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "unchanged")

	for i := 0; i < 3; i++ {
		if err := services.UpdatePoem(context.Background(), id, "unchanged"); err != nil {
			t.Fatalf("no-op update %d: %v", i, err)
		}
	}

	versions, err := services.ListPoemVersions(context.Background(), id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("version count = %d after three no-op saves, want 0", len(versions))
	}
}

// TestUpdateOfAnUnknownPoemReportsNotFound pins that a failed update records nothing.
//
// Without the transaction this is the case that produces a lying history: an insert keyed on a poem
// that does not exist would either fail outright or, with a loose foreign key, leave a version
// describing an edit that never happened.
func TestUpdateOfAnUnknownPoemReportsNotFound(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	err := services.UpdatePoem(context.Background(), "00000000-0000-0000-0000-000000000000", "anything")
	if !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("update of an unknown poem = %v, want ErrNotFound", err)
	}

	var count int
	if err := database.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM poem_versions`).Scan(&count); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if count != 0 {
		t.Fatalf("a failed update left %d version row(s); a history that records a change that never happened is worse than no history", count)
	}
}

// TestUpdateOfADeletedPoemReportsNotFound keeps a soft-deleted work out of the history, and confirms
// the deleted_at guard still holds now that the update path is a transaction.
func TestUpdateOfADeletedPoemReportsNotFound(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "about to be deleted")
	if err := services.SoftDeletePoem(context.Background(), id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	if err := services.UpdatePoem(context.Background(), id, "an edit to a deleted work"); !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("update of a deleted poem = %v, want ErrNotFound", err)
	}

	versions, err := services.ListPoemVersions(context.Background(), id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("a refused edit on a deleted poem recorded %d version(s)", len(versions))
	}
}

// TestRestoreBringsBackTheText pins the round trip: overwrite, then undo.
func TestRestoreBringsBackTheText(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "the draft worth keeping")
	if err := services.UpdatePoem(context.Background(), id, "a bad paste that replaced it"); err != nil {
		t.Fatalf("update: %v", err)
	}

	versions, err := services.ListPoemVersions(context.Background(), id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("version count = %d, want 1", len(versions))
	}

	if err := services.RestorePoemVersion(context.Background(), id, versions[0].ID); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := poemContentByID(t, id); got != "the draft worth keeping" {
		t.Fatalf("content after restore = %q, want the original draft back", got)
	}
}

// TestRestoreIsItselfUndoable is why a restore is an ordinary edit rather than a direct write.
//
// If restoring merely copied the old text in, the version it displaced would be gone and the restore
// would be a one-way door. Going through UpdatePoem means the bad paste is still recoverable.
func TestRestoreIsItselfUndoable(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "original")
	if err := services.UpdatePoem(context.Background(), id, "bad paste"); err != nil {
		t.Fatalf("update: %v", err)
	}

	versions, _ := services.ListPoemVersions(context.Background(), id)
	if err := services.RestorePoemVersion(context.Background(), id, versions[0].ID); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got := poemContentByID(t, id); got != "original" {
		t.Fatalf("content after restore = %q, want original", got)
	}

	// The bad paste must still be somewhere, so the restore can itself be undone.
	after, err := services.ListPoemVersions(context.Background(), id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("version count after a restore = %d, want 2; a restore must be undoable", len(after))
	}
	if after[0].Content != "bad paste" {
		t.Fatalf("newest version = %q, want the text the restore displaced", after[0].Content)
	}
}

// TestRestoreIsScopedToItsOwnPoem closes a data-corrupting path.
//
// GetPoemVersion is scoped by poem_id as well as version id. Without that scope, naming a version
// belonging to a different poem would restore this poem's content to that other poem's text.
func TestRestoreIsScopedToItsOwnPoem(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	owner := insertPoem(t, "owner draft one")
	other := insertPoem(t, "other draft one")

	if err := services.UpdatePoem(context.Background(), owner, "owner draft two"); err != nil {
		t.Fatalf("update owner: %v", err)
	}
	if err := services.UpdatePoem(context.Background(), other, "other draft two"); err != nil {
		t.Fatalf("update other: %v", err)
	}

	ownerVersions, _ := services.ListPoemVersions(context.Background(), owner)
	otherVersions, _ := services.ListPoemVersions(context.Background(), other)
	if len(ownerVersions) != 1 || len(otherVersions) != 1 {
		t.Fatalf("expected one version each, got %d and %d", len(ownerVersions), len(otherVersions))
	}

	// Ask to restore owner's version as though it belonged to `other`.
	err := services.RestorePoemVersion(context.Background(), other, ownerVersions[0].ID)
	if !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("restoring another poem's version = %v, want ErrNotFound", err)
	}

	if got := poemContentByID(t, other); got != "other draft two" {
		t.Fatalf("the other poem's content = %q, want it untouched", got)
	}
}

// TestRestoreOfAnUnknownVersionReportsNotFound keeps a mistyped id a clean miss.
func TestRestoreOfAnUnknownVersionReportsNotFound(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "untouched")

	err := services.RestorePoemVersion(context.Background(), id, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("restore of an unknown version = %v, want ErrNotFound", err)
	}
	if got := poemContentByID(t, id); got != "untouched" {
		t.Fatalf("content = %q, want it untouched", got)
	}
}

// TestVersionsOfOtherPoemsAreNotListed keeps the history per-poem.
func TestVersionsOfOtherPoemsAreNotListed(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	withHistory := insertPoem(t, "has history")
	without := insertPoem(t, "no history")

	if err := services.UpdatePoem(context.Background(), withHistory, "edited"); err != nil {
		t.Fatalf("update: %v", err)
	}

	versions, err := services.ListPoemVersions(context.Background(), without)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("a poem with no edits reported %d version(s)", len(versions))
	}
}

// TestUpdateSerializesConcurrentEditors proves the row lock does its job.
//
// Two concurrent edits each read the row to record what they are replacing. Without FOR UPDATE they
// can both read the same starting text, so one editor's starting point is recorded twice and the
// other editor's change never appears in the history -- the work is still in poems, but the history
// claims a sequence that did not happen.
func TestUpdateSerializesConcurrentEditors(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "starting text")

	const rounds = 12
	for i := 0; i < rounds; i++ {
		// Clean the slate each round: the assertion is about this pair of concurrent edits.
		if _, err := database.Pool.Exec(context.Background(),
			`DELETE FROM poem_versions`); err != nil {
			t.Fatalf("clear versions: %v", err)
		}
		if err := services.UpdatePoem(context.Background(), id, "starting text"); err != nil {
			t.Fatalf("reset: %v", err)
		}
		if _, err := database.Pool.Exec(context.Background(),
			`DELETE FROM poem_versions`); err != nil {
			t.Fatalf("clear versions: %v", err)
		}

		errs := make(chan error, 2)
		go func() { errs <- services.UpdatePoem(context.Background(), id, "editor A") }()
		go func() { errs <- services.UpdatePoem(context.Background(), id, "editor B") }()
		for e := 0; e < 2; e++ {
			if err := <-errs; err != nil {
				t.Fatalf("round %d concurrent update: %v", i, err)
			}
		}

		versions, err := services.ListPoemVersions(context.Background(), id)
		if err != nil {
			t.Fatalf("round %d list versions: %v", i, err)
		}
		if len(versions) != 2 {
			t.Fatalf("round %d: recorded %d versions after two concurrent edits, want 2; "+
				"one editor's starting point was lost", i, len(versions))
		}

		// The invariant is about the set, not the order. Whichever editor goes first records
		// "starting text" and writes its own draft; the second then records that draft. So the
		// history must hold the starting point plus exactly one of the two editors' texts -- never
		// the same text twice, which is the signature of a lost read.
		seen := map[string]int{}
		for _, v := range versions {
			seen[v.Content]++
		}
		if seen["starting text"] != 1 {
			t.Fatalf("round %d: the starting text appears %d times, want exactly 1; "+
				"two editors recorded the same starting point, so one read was lost",
				i, seen["starting text"])
		}
		if intermediates := seen["editor A"] + seen["editor B"]; intermediates != 1 {
			t.Fatalf("round %d: expected exactly one editor's text as the intermediate, got %d; versions = %v",
				i, intermediates, contentsOf(versions))
		}
		for content, n := range seen {
			if n != 1 {
				t.Fatalf("round %d: %q recorded %d times, want once; versions = %v",
					i, content, n, contentsOf(versions))
			}
		}
	}
}

// contentsOf renders a history for a failure message.
func contentsOf(versions []models.PoemVersion) []string {
	out := make([]string, 0, len(versions))
	for _, v := range versions {
		out = append(out, v.Content)
	}
	return out
}

// TestHistoryRoutesExerciseTheFeature covers the HTTP surface, not just the service layer.
//
// The service tests prove the data is retained. These prove it is reachable: a route that 404s, or a
// restore that is refused for want of a session, would leave the retention working and the feature
// unusable.
func TestHistoryRoutesExerciseTheFeature(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "the draft worth keeping")
	if err := services.UpdatePoem(context.Background(), id, "a bad paste"); err != nil {
		t.Fatalf("update: %v", err)
	}
	versions, err := services.ListPoemVersions(context.Background(), id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}

	srv := newTestServer(t)

	status, body, _ := get(t, srv.URL+"/poem/"+id+"/history", nil)
	if status != 200 {
		t.Fatalf("GET /poem/{id}/history = %d, want 200; body: %s", status, truncate([]byte(body)))
	}
	if !strings.Contains(body, "the draft worth keeping") {
		t.Error("the history screen does not show the retained draft")
	}

	// Restore through the endpoint, the way the form does it.
	status, body, _ = postForm(t, srv.URL+"/poem/restore", url.Values{
		"id":      {id},
		"version": {versions[0].ID},
	}, nil)
	if status != 200 {
		t.Fatalf("POST /poem/restore = %d, want 200; body: %s", status, truncate([]byte(body)))
	}
	if got := poemContentByID(t, id); got != "the draft worth keeping" {
		t.Fatalf("content after restoring over HTTP = %q, want the retained draft", got)
	}
}

// TestHistoryOfADeletedPoemStaysReachable is the recovery path.
//
// A soft-deleted work is hidden from the library, and its history has to remain reachable anyway --
// requiring the poem to be active would make recovery impossible at exactly the moment it is wanted.
func TestHistoryOfADeletedPoemStaysReachable(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "an accidental deletion")
	if err := services.SoftDeletePoem(context.Background(), id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	srv := newTestServer(t)

	status, body, _ := get(t, srv.URL+"/poem/"+id+"/history", nil)
	if status != 200 {
		t.Fatalf("GET history of a deleted poem = %d, want 200; the recovery path must stay reachable",
			status)
	}
	if !strings.Contains(body, "an accidental deletion") {
		t.Error("the history of a deleted poem does not show the work")
	}
}

// TestHistoryRequiresASession keeps the retained versions as private as the work itself.
func TestHistoryRequiresASession(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "private work")
	srv := newLoginServer(t, true)
	c := newAnonymousClient(t, srv)
	// Not following the redirect, so the assertion is about requireAuth's own response rather than
	// whatever it points at.
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp := doGet(t, c, srv.URL+"/poem/"+id+"/history")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET history without a session = %d, want 303 to the login page", resp.StatusCode)
	}
	if strings.Contains(string(body), "private work") {
		t.Fatal("a retained draft was served to an unauthenticated request")
	}
	// And following it must not disclose the work either.
	followed := doGet(t, c, srv.URL+"/login")
	defer followed.Body.Close()
	loginBody, _ := io.ReadAll(followed.Body)
	if strings.Contains(string(loginBody), "private work") {
		t.Fatal("a retained draft was served to an unauthenticated request")
	}
}
