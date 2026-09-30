package publish_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/migrate"
	"github.com/divijg19/Verse/internal/publish"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/internal/testsupport"
)

// TestTheDryRunWritesNothing is the property the whole package exists to guarantee, checked from the
// database's side rather than by reading the source.
//
// A dry-run that publishes works is worse than no dry-run, because the author ran the safe thing
// expecting nothing to happen. Asserting "this function only SELECTs" by reading it is a claim about
// the current text; the way to make it a claim about the operation is to run it and then ask the
// database what changed.
//
// Compared as full row contents, not as a count. A count would be satisfied by a write that changed a
// row in place, which is the more likely mistake here -- a stray UPDATE published_at rather than an
// accidental INSERT -- and would report nothing wrong.
func TestTheDryRunWritesNothing(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "dryrun_nowrite")

	// Three different states, so a write that touched any of them changes the snapshot: a draft, a
	// published work, and a published-then-deleted work.
	seed(t, ctx, scratch,
		seedPoem{id: "aaaaaaaa-0000-0000-0000-000000000001", content: "a draft"},
		seedPoem{id: "aaaaaaaa-0000-0000-0000-000000000002", content: "a published work", published: true},
		seedPoem{id: "aaaaaaaa-0000-0000-0000-000000000003", content: "a deleted work", published: true, deleted: true},
	)

	before := snapshot(t, ctx, scratch)
	if len(before) != 3 {
		t.Fatalf("expected 3 seeded works, got %d", len(before))
	}

	result, err := publish.DryRun(ctx)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result.Count != 1 {
		t.Errorf("expected the dry-run to report 1 published work, got %d", result.Count)
	}
	if !result.Complete() {
		t.Errorf("the dry-run reported %d entries against a counted %d", result.Count, result.Counted)
	}

	if diff := diff(before, snapshot(t, ctx, scratch)); diff != "" {
		t.Errorf("the dry-run modified the library:\n%s", diff)
	}
}

// TestTheDryRunReportsRealSizesAndTitles, end to end through the real query rather than an injected
// reader.
//
// The unit tests in the package cover the field derivation from a synthetic Poem; this covers the
// path an author actually takes -- four columns scanned positionally from Postgres into the entry --
// which is where a mismatch between a query's column list and a scan would surface, and it would
// surface as a runtime error rather than a wrong number.
func TestTheDryRunReportsRealSizesAndTitles(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "dryrun_fields")

	const content = "A Title Worth Reading\n\nthe body of the work, which is long enough to matter"
	seed(t, ctx, scratch, seedPoem{
		id: "bbbbbbbb-0000-0000-0000-000000000001", content: content, published: true,
	})

	result, err := publish.DryRun(ctx)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	entry := result.Entries[0]

	if want := "A Title Worth Reading"; entry.Title != want {
		t.Errorf("title is %q, want %q", entry.Title, want)
	}
	if want := len(content); entry.SizeBytes != want {
		t.Errorf("size is %d bytes, want %d", entry.SizeBytes, want)
	}
	if entry.PublishedAt.IsZero() {
		t.Error("PublishedAt is zero; the publisher needs the instant to date the work in a sitemap")
	}
	if entry.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
	if want := "bbbbbbbb-0000-0000-0000-000000000001"; entry.ID != want {
		t.Errorf("id is %q, want %q", entry.ID, want)
	}
}

// TestTheDryRunNeverShowsTheAuthorAWorkTwice, aimed at the ordering.
//
// A listing that repeats a work, or drops one, is the same class of bug as the truncation check
// above, and the ordering is what governs it: published_at DESC with an id tiebreak is a total order,
// so the same library always produces the same sequence. Published instants are identical in this
// fixture on purpose, which is the case that makes an unstable sort visible.
func TestTheDryRunOrderIsTotal(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "dryrun_order")

	// Identical published_at for every row, so any ordering at all is produced by the tiebreak alone.
	for _, id := range []string{
		"cccccccc-0000-0000-0000-000000000001",
		"cccccccc-0000-0000-0000-000000000002",
		"cccccccc-0000-0000-0000-000000000003",
		"cccccccc-0000-0000-0000-000000000004",
		"cccccccc-0000-0000-0000-000000000005",
	} {
		seed(t, ctx, scratch, seedPoem{
			id: id, content: "work " + id, published: true,
			publishedAt: "2026-03-01T12:00:00Z",
		})
	}

	first, err := publish.DryRun(ctx)
	if err != nil {
		t.Fatalf("first dry run: %v", err)
	}
	if first.Count != 5 {
		t.Fatalf("expected 5 entries, got %d", first.Count)
	}

	seen := map[string]bool{}
	for i, e := range first.Entries {
		if seen[e.ID] {
			t.Errorf("work %s appears twice in one dry-run", e.ID)
		}
		seen[e.ID] = true
		if i > 0 {
			prev, cur := first.Entries[i-1].ID, e.ID
			if prev < cur {
				t.Errorf("entries are not in id-descending order: %s came before %s", prev, cur)
			}
		}
	}

	// Same input, same order. Not a strong claim on its own, but the two would differ if the sort
	// were not total, and a static build's output has to be reproducible for the export check to mean
	// anything.
	second, err := publish.DryRun(ctx)
	if err != nil {
		t.Fatalf("second dry run: %v", err)
	}
	for i := range first.Entries {
		if first.Entries[i].ID != second.Entries[i].ID {
			t.Errorf("two dry-runs over the same library ordered differently at position %d: %s then %s",
				i, first.Entries[i].ID, second.Entries[i].ID)
		}
	}
}

// TestTheDryRunOnAFreshLibraryIsEmpty is the state the split begins in.
//
// Every row is NULL after the migration, so the first reader build has nothing to serve. An empty
// result is the correct answer and must not be an error, a nil slice, or a non-empty list -- and it is
// worth stating as a test because "the site is empty" is indistinguishable from "the query is broken"
// if nothing says which it is.
func TestTheDryRunOnAFreshLibraryIsEmpty(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "dryrun_empty")

	seed(t, ctx, scratch,
		seedPoem{id: "dddddddd-0000-0000-0000-000000000001", content: "a draft"},
		seedPoem{id: "dddddddd-0000-0000-0000-000000000002", content: "another draft"},
	)

	result, err := publish.DryRun(ctx)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result.Count != 0 || result.Counted != 0 {
		t.Errorf("expected an empty result from a library of drafts, got %d and %d", result.Count, result.Counted)
	}
	if !result.Complete() {
		t.Error("an empty library was reported as incomplete")
	}
	if result.Entries == nil {
		t.Error("Entries is nil; an empty dry-run should still be a usable empty slice")
	}
	if _, err := services.ListPublishedPoems(ctx); err != nil {
		t.Errorf("the publisher's read failed on an empty library: %v", err)
	}
}

// TestTheDryRunListsALargeLibraryInFull is the integration counterpart of the unit test for
// truncation, on the real query.
//
// 150 published works against the 100-row default that listLivePoems uses. The point is that
// ListPublishedPoems has no limit while every other read in this repository does, and the failure a
// future LIMIT would cause is a static site that looks complete and is missing 50 poems.
func TestTheDryRunListsALargeLibraryInFull(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "dryrun_large")

	if _, err := scratch.Exec(ctx, `
        INSERT INTO poems (id, content, published_at)
        SELECT gen_random_uuid(), 'work ' || n, now()
        FROM generate_series(1, 150) n`); err != nil {
		t.Fatalf("seed 150 published works: %v", err)
	}

	result, err := publish.DryRun(ctx)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result.Count != 150 {
		t.Errorf("expected all 150 published works, got %d.\n"+
			"  The publisher's read has no limit by design: a bounded archive reports a partial\n"+
			"  library as the whole of it, and nothing downstream reports the difference.", result.Count)
	}
	if !result.Complete() {
		t.Errorf("a 150-work library reported %d entries against a counted %d", result.Count, result.Counted)
	}
}

// TestTheDryRunDoesNotPublishOnItsOwn, from the other direction: the author's library and the
// publisher's must be different sets, and a dry-run must not narrow the former.
//
// The asymmetry is the whole design. A draft is not deleted and is not hidden -- the author can see
// and open it exactly as before -- while the publisher cannot see it. If either half of that
// regressed, this release's central claim would be false.
func TestTheDraftsStayVisibleToTheAuthor(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "asymmetry")

	seed(t, ctx, scratch,
		seedPoem{id: "eeeeeeee-0000-0000-0000-000000000001", content: "a published work", published: true},
		seedPoem{id: "eeeeeeee-0000-0000-0000-000000000002", content: "a draft"},
		seedPoem{id: "eeeeeeee-0000-0000-0000-000000000003", content: "a deleted work", deleted: true},
	)

	// The publisher sees one.
	result, err := publish.DryRun(ctx)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result.Count != 1 {
		t.Errorf("the publisher should see 1 work, got %d", result.Count)
	}

	// The author sees the published work and the draft, and not the deleted one -- unchanged by the
	// column existing at all.
	works, err := services.ListPoems(ctx, 100, 0)
	if err != nil {
		t.Fatalf("list the author's library: %v", err)
	}
	if len(works) != 2 {
		var contents []string
		for _, w := range works {
			contents = append(contents, w.Content)
		}
		t.Errorf("expected the author's library to hold 2 works, got %d: %v", len(works), contents)
	}

	// And the draft is individually readable, not merely counted. A draft that appears in a list but
	// 404s on open is the exact regression the split must not introduce.
	draft, err := services.GetPoem(ctx, "eeeeeeee-0000-0000-0000-000000000002")
	if err != nil {
		t.Errorf("the author cannot open their own draft: %v", err)
	} else if draft.Content != "a draft" {
		t.Errorf("GetPoem returned %q for the draft", draft.Content)
	}
}

// seedPoem is one row for the fixtures above.
//
// publishedAt is separate from the published flag so the ordering test can give every row the same
// instant, which is the case that exposes a non-total sort.
type seedPoem struct {
	id          string
	content     string
	published   bool
	publishedAt string
	deleted     bool
}

func seed(t *testing.T, ctx context.Context, scratch *pgxpool.Pool, rows ...seedPoem) {
	t.Helper()
	for _, r := range rows {
		var pub, del any
		if r.publishedAt != "" {
			pub = r.publishedAt
		} else if r.published {
			pub = time.Now().UTC()
		}
		if r.deleted {
			del = time.Now().UTC()
		}
		if _, err := scratch.Exec(ctx, `
            INSERT INTO poems (id, content, published_at, deleted_at)
            VALUES ($1, $2, $3, $4)`, r.id, r.content, pub, del); err != nil {
			t.Fatalf("seed %s: %v", r.id, err)
		}
	}
}

// snapshot is every column of every row, so a change to any value is visible.
type snapshotRow struct {
	content     string
	publishedAt *time.Time
	deletedAt   *time.Time
}

func snapshot(t *testing.T, ctx context.Context, scratch *pgxpool.Pool) map[string]snapshotRow {
	t.Helper()
	rows, err := scratch.Query(ctx, `SELECT id, content, published_at, deleted_at FROM poems ORDER BY id`)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer rows.Close()

	out := map[string]snapshotRow{}
	for rows.Next() {
		var id string
		var r snapshotRow
		if err := rows.Scan(&id, &r.content, &r.publishedAt, &r.deletedAt); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[id] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot rows: %v", err)
	}
	return out
}

// diff describes how two snapshots differ, or returns "" when they are identical.
//
// A map diff rather than bytes.Equal, so the failure names the row and the column. The failure being
// understood is most of the value of a test like this, and the assertion that matters is rare enough
// that it should not be a bisect.
func diff(before, after map[string]snapshotRow) string {
	var b strings.Builder
	for id, a := range after {
		prev, ok := before[id]
		if !ok {
			fmt.Fprintf(&b, "  %s was created\n", id)
			continue
		}
		if prev.content != a.content {
			fmt.Fprintf(&b, "  %s content changed from %q to %q\n", id, prev.content, a.content)
		}
		// time.Time is compared with Equal, not ==. A value read from two queries on the same
		// connection can carry a different monotonic reading or location, and == reports those as
		// different instants when they are the same one -- which would make this helper report a
		// change that never happened, and a test that cries wolf about no writes is a test that
		// gets ignored.
		if !sameInstant(prev.publishedAt, a.publishedAt) {
			fmt.Fprintf(&b, "  %s published_at changed from %v to %v\n", id, prev.publishedAt, a.publishedAt)
		}
		if !sameInstant(prev.deletedAt, a.deletedAt) {
			fmt.Fprintf(&b, "  %s deleted_at changed from %v to %v\n", id, prev.deletedAt, a.deletedAt)
		}
	}
	for id := range before {
		if _, ok := after[id]; !ok {
			fmt.Fprintf(&b, "  %s was deleted\n", id)
		}
	}
	return b.String()
}

// requireDSN and newScratch mirror the tests package's helpers, in a package this file can see.
//
// Duplicated rather than exported from tests, because tests/ is a test-only package: it has no
// non-test consumers and nothing outside it can import these. A shared helper belongs in
// testsupport, which both already depend on, and promoting it there is a mechanical follow-up rather
// than a design decision this release should be making.
func requireDSN(t *testing.T) string {
	t.Helper()
	dsn, reason := testsupport.DisposableDSN()
	if reason != "" {
		t.Skip(reason)
	}
	return dsn
}

func newScratch(t *testing.T, dsn, tag string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	schema := testsupport.NormalizeSchemaName(tag+"_", t.Name())
	scratch, cleanup, err := testsupport.ConnectScratch(ctx, dsn, schema)
	if err != nil {
		t.Fatalf("scratch schema: %v", err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("drop the scratch schema: %v", err)
		}
	})

	// ConnectScratch creates an empty schema; the migrations have to be applied to it before any test
	// can name a table. The runner is used rather than a hand-rolled copy of the migration files, so a
	// new migration is picked up here without editing a test -- the reason applyMigrationsExcept in
	// the tests package takes an exclusion is that it is deliberately reaching around the runner.
	if _, err := migrate.Run(ctx, scratch); err != nil {
		_ = cleanup()
		t.Fatalf("apply the migrations to %s: %v", schema, err)
	}

	previous := database.Pool
	database.Pool = scratch
	t.Cleanup(func() { database.Pool = previous })
	return scratch
}

// sameInstant compares two nullable timestamps by the instant they name, treating both-nil as equal.
//
// Both-nil is the case that matters: published_at IS NULL is the normal state for a draft, and
// comparing two nils by == happens to work but only by accident of the representation.
func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
