package tests

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/migrate"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/internal/testsupport"
)

// The migration this release ships, by name -- the same treatment 006 gets, so a rename fails here
// rather than leaving these tests applying every file and asserting nothing.
const publishedAtMigration = "007_published_at.sql"

// newMigratedScratch is a scratch schema with every migration applied, plus the pool swap that lets
// the services under test see it.
//
// The migrations are applied through the real runner rather than by executing the embedded files, so
// a new migration is picked up without editing a test. applyMigrationsExcept below deliberately
// reaches around the runner -- that is its whole job -- and it would be the wrong tool here.
//
// Returns a cleanup rather than registering it, because the callers in this file want the pool swap
// and the schema teardown to be visibly paired at the point of use.
func newMigratedScratch(t *testing.T, dsn, tag string) (*pgxpool.Pool, func(*testing.T)) {
	t.Helper()
	ctx := context.Background()

	schema := testsupport.NormalizeSchemaName(tag+"_", t.Name())
	scratch, drop, err := testsupport.ConnectScratch(ctx, dsn, schema)
	if err != nil {
		t.Fatalf("scratch schema: %v", err)
	}
	if _, err := migrate.Run(ctx, scratch); err != nil {
		_ = drop()
		t.Fatalf("apply the migrations to %s: %v", schema, err)
	}

	// Every service in this repository reads database.Pool rather than taking one as an argument,
	// which is what lets export.Build be called from a command with no plumbing. The cost is that a
	// test exercising those services has to swap the global -- safe while the suite is sequential,
	// and asserted by TestTheSuiteIsSequential so it cannot stop being safe unnoticed.
	previous := database.Pool
	database.Pool = scratch

	return scratch, func(t *testing.T) {
		database.Pool = previous
		if err := drop(); err != nil {
			t.Errorf("drop the scratch schema: %v", err)
		}
	}
}

// TestThePublicationMigrationIsNonDestructive is the operator-facing guarantee for 007: applying it
// changes nothing about stored data.
//
// 006 needed this because it rewrote every timestamp in the table. 007 does not touch a single stored
// value, and this asserts that by the strongest available measure rather than by inspection: the
// export -- the author's only copy of their work, one-way, with no import path to recover it -- is
// byte-for-byte identical before and after.
//
// Worth having anyway, because "adds a nullable column" is the kind of migration whose cost gets
// assumed rather than measured. If a future edit adds a DEFAULT, a backfill, or a rewrite, the export
// changes and this fails. The migration's safety argument is the absence of a default, so the absence
// is what gets tested.
func TestThePublicationMigrationIsNonDestructive(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()

	schema := testsupport.NormalizeSchemaName("publish_migration_t_", t.Name())
	scratch, cleanup, err := testsupport.ConnectScratchInZone(ctx, dsn, schema, "UTC")
	if err != nil {
		t.Fatalf("scratch schema: %v", err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("drop the scratch schema: %v", err)
		}
	})

	normalize := applyMigrationsExcept(t, scratch, publishedAtMigration)
	seedTimestampsForMigration(t, scratch)

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	before := exportWithPinnedTimestamp(t)
	if _, err := scratch.Exec(ctx, normalize, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("apply %s: %v", publishedAtMigration, err)
	}
	after := exportWithPinnedTimestamp(t)

	if bytes.Equal(before, after) {
		return
	}
	t.Errorf("the export changed across %s: %d bytes before, %d after\n  first difference at byte %d\n  before: %q\n  after:  %q",
		publishedAtMigration, len(before), len(after), firstDifference(before, after),
		windowAround(before, firstDifference(before, after)),
		windowAround(after, firstDifference(before, after)))
}

// TestThePublicationMigrationPublishesNothing is the half of the migration's safety argument that
// byte-identity above cannot cover.
//
// A nullable column with no default leaves every existing row NULL -- a claim about the *data*, not
// about the file. This checks it directly: rows that existed before the migration are still all
// drafts after it, and the publisher's read returns none of them.
//
// The failure it prevents is the one irreversible mistake in the project. A `DEFAULT now()` added by
// someone who assumed existing rows should be visible would not change the export at all -- the
// export does not carry published_at yet -- so the test above would pass, and the next deploy would
// publish the author's entire private library. Green tests, total disclosure.
func TestThePublicationMigrationPublishesNothing(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()

	// Every migration except the one under test, so this schema is at the pre-migration shape and the
	// real file is what moves it forward. Reading the statement out of the migration by hand and
	// re-running it here would be a re-implementation: a test that re-types the thing it is testing
	// passes when the file says something different, which is the failure this one exists to catch.
	schema := testsupport.NormalizeSchemaName("pubnone_t_", t.Name())
	scratch, cleanup, err := testsupport.ConnectScratchInZone(ctx, dsn, schema, "UTC")
	if err != nil {
		t.Fatalf("scratch schema: %v", err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("drop the scratch schema: %v", err)
		}
	})
	normalize := applyMigrationsExcept(t, scratch, publishedAtMigration)

	// Real works rather than one token row, so the count is worth comparing.
	for i := 0; i < 3; i++ {
		if _, err := scratch.Exec(ctx, `
            INSERT INTO poems (id, content, created_at) VALUES ($1, $2, $3)`,
			"77777777-7777-7777-7777-77777755555"+string(rune('0'+i)),
			"a work that predates publication", time.Unix(int64(1700000000+i), 0).UTC()); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	var seeded int
	if err := scratch.QueryRow(ctx, `SELECT count(*) FROM poems`).Scan(&seeded); err != nil {
		t.Fatalf("count the seeded works: %v", err)
	}
	if seeded != 3 {
		t.Fatalf("expected 3 seeded works, found %d", seeded)
	}

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	// The real migration, not a copy of its statement.
	if _, err := scratch.Exec(ctx, normalize, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("apply %s: %v", publishedAtMigration, err)
	}

	var published, nulls, total int64
	if err := scratch.QueryRow(ctx, `
        SELECT count(*) FILTER (WHERE published_at IS NOT NULL),
               count(*) FILTER (WHERE published_at IS NULL),
               count(*)
        FROM poems`).Scan(&published, &nulls, &total); err != nil {
		t.Fatalf("inspect published_at: %v", err)
	}

	if published != 0 {
		t.Errorf("%d of %d pre-existing works became published by applying the migration.\n"+
			"  Every existing row must land NULL: publication is an explicit act by the author, and a\n"+
			"  migration that publishes their private library is not reversible by redeploying.",
			published, total)
	}
	if nulls != total {
		t.Errorf("expected all %d rows to have published_at NULL, found %d NULL", total, nulls)
	}

	// The data claim is necessary but not sufficient: what matters is what the publisher can read.
	works, err := services.ListPublishedPoems(ctx)
	if err != nil {
		t.Fatalf("read published works: %v", err)
	}
	if len(works) != 0 {
		t.Errorf("the publisher's read returned %d works immediately after the migration; it must return none until the author publishes something", len(works))
	}
}

// TestThePublishedMigrationHasNoDefault is aimed at the exact form that mistake would take.
//
// The test above checks the *effect* on rows, which is what matters. This checks the cause, so that
// someone adding `DEFAULT now()` in order to fix some unrelated problem is stopped by a failure that
// names the mistake rather than by an indirect one three releases later.
//
// pg_attribute.attnotnull is false and atthasdef is false, read from the catalog rather than inferred
// from a missing value in a SELECT -- because "every row is NULL" and "the column has no default" are
// different facts, and only the second one is what stops a future INSERT from being published by
// accident either.
func TestThePublishedMigrationHasNoDefault(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()

	scratch, done := newMigratedScratch(t, dsn, "pubnodefault")
	defer done(t)

	var hasDefault bool
	var typeName string
	if err := scratch.QueryRow(ctx, `
        SELECT a.atthasdef, format_type(a.atttypid, a.atttypmod)
        FROM pg_attribute a
        WHERE a.attrelid = 'poems'::regclass
          AND a.attname = 'published_at'`).Scan(&hasDefault, &typeName); err != nil {
		t.Fatalf("read the published_at column from the catalog: %v", err)
	}

	if hasDefault {
		t.Error("poems.published_at has a DEFAULT.\n" +
			"  It must not. A default of now() would publish the whole existing library on the next\n" +
			"  deploy, and would publish every future INSERT that omits the column. Publication is an\n" +
			"  explicit act; the column stays NULL until the author performs it.")
	}
	if want := "timestamp with time zone"; typeName != want {
		t.Errorf("published_at is %q, want %q. TIMESTAMPTZ matches deleted_at and survives a\n"+
			"  timezone change; a naive TIMESTAMP would silently reinterpret existing instants.", typeName, want)
	}
}

// TestThePublishedFilterExcludesDraftsAndDeletedWorks pins the two exclusions separately, because they
// are not redundant and a future edit could drop either one.
//
// A work can be published and then soft-deleted. Filtering only on published_at serves a deleted work,
// which is the single outcome the split exists to prevent; filtering only on deleted_at serves every
// draft. Neither failure is visible in the author's app, because a draft is not deleted and a deleted
// work was not supposed to be public -- both only become visible on the public site, which is exactly
// where a mistake in this project is least recoverable.
//
// Each row is built so the other predicate would have included it, so a regression is a hard failure
// rather than a row that happens not to exist.
func TestThePublishedFilterExcludesDraftsAndDeletedWorks(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()

	scratch, done := newMigratedScratch(t, dsn, "pubfilter")
	defer done(t)

	// Four combinations, so each exclusion is load-bearing for at least one row.
	if _, err := scratch.Exec(ctx, `
        INSERT INTO poems (id, content, published_at, deleted_at) VALUES
            ('88888888-8888-8888-8888-888888888881', 'published then deleted', now(), now()),
            ('88888888-8888-8888-8888-888888888882', 'published and live',        now(), NULL),
            ('88888888-8888-8888-8888-888888888883', 'draft and live',           NULL, NULL),
            ('88888888-8888-8888-8888-888888888884', 'draft and deleted',       NULL, now())`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	works, err := services.ListPublishedPoems(ctx)
	if err != nil {
		t.Fatalf("read published works: %v", err)
	}
	if len(works) != 1 {
		var contents []string
		for _, w := range works {
			contents = append(contents, w.Content)
		}
		t.Errorf("expected exactly the one published and live work, got %d: %v", len(works), contents)
	}
	if len(works) == 1 {
		if works[0].Content != "published and live" {
			t.Errorf("returned the wrong work: %q", works[0].Content)
		}
		if works[0].DeletedAt != nil {
			t.Errorf("returned a work with a non-nil DeletedAt: %v", works[0].DeletedAt)
		}
		if works[0].PublishedAt == nil {
			t.Error("returned a work with a nil PublishedAt; the publisher needs the instant for a sitemap's lastmod")
		}
	}

	// COUNT and the list must agree, or one of them is wrong and the dry-run's cross-check is
	// comparing two different sets.
	count, err := services.CountPublishedPoems(ctx)
	if err != nil {
		t.Fatalf("count published works: %v", err)
	}
	if count != len(works) {
		t.Errorf("CountPublishedPoems says %d but ListPublishedPoems returned %d", count, len(works))
	}
}

// TestListPublishedPoemsNeverTruncates is aimed at one specific future mistake: someone adding a LIMIT
// to this query for performance, which is the obvious next idea and would be wrong.
//
// listLivePoems caps at 100 rows and is right to, because its callers want a page. The publisher wants
// everything, and a capped archive is not a smaller archive -- it is a site that looks complete and is
// missing work, with nothing in the build output saying so. That is the failure RISK_REGISTER flags
// against silently truncating the public archive.
//
// 150 rows rather than 101, so the assertion is not adjacent to the boundary, and both the length and
// the agreeing count are checked, since a truncation has to break one or the other to go unnoticed.
func TestListPublishedPoemsNeverTruncates(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()

	scratch, done := newMigratedScratch(t, dsn, "pubtrunc")
	defer done(t)

	// One statement, so seeding 150 rows is not what is slow.
	if _, err := scratch.Exec(ctx, `
        INSERT INTO poems (id, content, published_at)
        SELECT gen_random_uuid(), 'work ' || n, now()
        FROM generate_series(1, 150) n`); err != nil {
		t.Fatalf("seed 150 published works: %v", err)
	}

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	works, err := services.ListPublishedPoems(ctx)
	if err != nil {
		t.Fatalf("read published works: %v", err)
	}
	if len(works) != 150 {
		t.Errorf("expected all 150 published works, got %d.\n"+
			"  The publisher's read has no limit by design: a bounded archive reports a partial\n"+
			"  library as the whole of it, and nothing downstream reports the difference.", len(works))
	}

	count, err := services.CountPublishedPoems(ctx)
	if err != nil {
		t.Fatalf("count published works: %v", err)
	}
	if count != len(works) {
		t.Errorf("CountPublishedPoems says %d but ListPublishedPoems returned %d", count, len(works))
	}
}

// TestTheAuthorsLibraryIsUnaffectedByPublication is the property that makes the split need no status
// badge, filter, or count anywhere in the authoring app, asserted rather than assumed.
//
// D10's concern was that adding a publication status would make the authoring app *worse* by hiding
// work the author cannot see. That is a real risk under a works-table design, where moving rows and
// adding a status can leave work somewhere the existing queries do not reach.
//
// It cannot happen here, and the reason is structural: ListPoems filters on deleted_at only, so a
// draft is a row it returns exactly as it did before the column existed. Worth pinning because it is
// the assumption the whole no-UI-change argument rests on.
func TestTheAuthorsLibraryIsUnaffectedByPublication(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()

	scratch, done := newMigratedScratch(t, dsn, "pubauthor")
	defer done(t)

	if _, err := scratch.Exec(ctx, `
        INSERT INTO poems (id, content, published_at, deleted_at) VALUES
            ('99999999-9999-9999-9999-999999999991', 'a published work', now(), NULL),
            ('99999999-9999-9999-9999-999999999992', 'a draft',         NULL, NULL),
            ('99999999-9999-9999-9999-999999999993', 'a deleted work',  now(), now())`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	works, err := services.ListPoems(ctx, 100, 0)
	if err != nil {
		t.Fatalf("list the author's library: %v", err)
	}
	if len(works) != 2 {
		var contents []string
		for _, w := range works {
			contents = append(contents, w.Content)
		}
		t.Errorf("expected the author's library to hold 2 works (1 published, 1 draft), got %d: %v",
			len(works), contents)
	}

	// The draft must be readable individually, not merely counted: a draft that appears in a list
	// but 404s on open would be the exact regression this test exists to prevent.
	draft, err := services.GetPoem(ctx, "99999999-9999-9999-9999-999999999992")
	if err != nil {
		t.Errorf("the author cannot open their own draft: %v", err)
	} else if draft.Content != "a draft" {
		t.Errorf("GetPoem returned %q for the draft", draft.Content)
	}

	// And the search box, which is the other read a hidden draft would disappear from.
	found, err := services.SearchPoems(ctx, "a draft", 100, 0)
	if err != nil {
		t.Fatalf("search the author's library: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("expected the search box to find the author's draft, got %d results", len(found))
	}
}

// TestThePublishedFilterIsTheOnlySpellingOfIt keeps the publisher's predicate from drifting into a
// second literal somewhere in the codebase.
//
// A security boundary expressed as four near-identical strings is four chances to be wrong, and the
// wrong ones would be invisible: a draft that leaks to the public site is a content bug with no
// failing test and no error message. This is a source-level check rather than a behavioral one, so
// it is worth being explicit about its limits -- it does not prove any query is correct, and it would
// miss a duplicated filter written differently. What it catches is the common quiet failure: someone
// hand-writing the predicate instead of using the constant.
func TestThePublishedFilterIsTheOnlySpellingOfIt(t *testing.T) {
	// models/poem.go defines the constant, so it is the one file that necessarily spells the
	// predicate out in full.
	constantsFile := "internal/models/poem.go"

	matches, err := grepSourceLines("published_at IS NOT NULL")
	if err != nil {
		t.Fatalf("walk the source tree: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("the pattern matched nothing; this test is passing without checking anything")
	}

	for _, m := range matches {
		if m.file == constantsFile {
			continue
		}
		// A reference to the constant is the correct spelling, however the query is composed.
		if strings.Contains(m.line, "models.PublishedFilter") {
			continue
		}
		t.Errorf("%s:%d spells the publisher's filter out again:\n  %s\n"+
			"  Use models.PublishedFilter. The predicate is the security boundary of the split, and a\n"+
			"  second literal is a second place for a draft to leak through.", m.file, m.lineNo, strings.TrimSpace(m.line))
	}
}

// TestAddingPublishedAtChangesNoExistingRead is the structural version of the same no-UI-change claim.
//
// It asserts the fact that makes every existing read immune: the migration adds a column, and no query
// anywhere uses SELECT *. A read that names its columns cannot be affected by a column that did not
// exist when it was written. A read that does not would start returning a field it never scanned for,
// and how that fails depends on the driver -- silently in some cases, as a scan error in others.
//
// The audit that motivated this found zero SELECT * by grep and believed it. This makes it a property
// of the repository rather than of a search somebody remembered to run, because the cost of a SELECT *
// here is paid the next time a column is added, not the next time this file is read.
func TestAddingPublishedAtChangesNoExistingRead(t *testing.T) {
	matches, linesScanned, filesScanned, err := grepSourceLinesCounted("SELECT *")
	if err != nil {
		t.Fatalf("walk the source tree: %v", err)
	}

	// Assert the walk was live, not that a bare SELECT * exists.
	//
	// Verified missing: without this the test PASSES on a pattern no file contains, which was
	// demonstrated by pointing the needle at a string nothing in the repository has. No matches
	// means no errors, and no errors is indistinguishable from "the repository is clean".
	//
	// The assertion is on lines examined rather than on a positive match count, because a correct
	// repository has zero bare SELECT * -- COUNT(*) is legitimate and gets filtered out below -- so
	// demanding a match would fail on the state this test exists to confirm. What has to be proven is
	// that the walk covered the source at all.
	if linesScanned == 0 {
		t.Fatal("the walk examined no lines, so the pattern was never tested against anything")
	}
	t.Logf("walked %d lines across %d files", linesScanned, filesScanned)

	// A hit is not automatically a bug. COUNT(*) is legitimate, and so is SELECT * in a statement that
	// never scans a row positionally. What cannot be legitimate is a bare `SELECT *` whose rows are
	// scanned positionally, because that is the read a new column silently breaks -- so the filter
	// here is deliberately coarse, and a false positive is a one-line fix rather than a missed bug.
	for _, m := range matches {
		upper := strings.ToUpper(strings.TrimSpace(m.line))
		if strings.Contains(upper, "COUNT(") || strings.Contains(upper, "EXISTS") {
			continue
		}
		t.Errorf("%s:%d uses SELECT *:\n  %s\n"+
			"  Name the columns. Every read in this repository names its columns, which is why adding\n"+
			"  published_at changed no existing read; a bare SELECT * breaks that the next time a column\n"+
			"  is added, and the scan that follows it is positional.", m.file, m.lineNo, strings.TrimSpace(m.line))
	}
}
