package migrate

import (
	"context"
	"path"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/internal/testsupport"
	"github.com/divijg19/Verse/migrations"
)

// Each test below runs against its own freshly created schema, dropped afterwards. The migrations
// under test include CREATE TABLE and DROP SCHEMA-level effects, so sharing the schema the rest of
// the suite truncates would make these tests capable of breaking unrelated ones.
//
// The gate is the same one the rest of the destructive suite uses: a dedicated test DSN plus an
// explicit acknowledgement, because creating and dropping schemas is destructive too.

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn, reason := testsupport.DisposableDSN()
	if reason != "" {
		t.Skip(reason)
	}

	schema := testsupport.NormalizeSchemaName("migrate_t_", t.Name())

	pool, cleanup, err := testsupport.ConnectScratch(context.Background(), dsn, schema)
	if err != nil {
		t.Fatalf("scratch schema: %v", err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("scratch schema cleanup: %v", err)
		}
	})

	return pool
}

// --- 1. A fresh database gets every migration, and a second run applies nothing ---

func TestRunAppliesPendingThenSkipsAll(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	first, err := run(ctx, pool, migrations.FS)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(first.Applied) == 0 {
		t.Fatal("first run applied nothing")
	}
	if len(first.Skipped) != 0 {
		t.Fatalf("first run skipped %v, want nothing skipped", first.Skipped)
	}

	// Sorted, and every embedded file accounted for.
	for i := 1; i < len(first.Applied); i++ {
		if first.Applied[i-1] > first.Applied[i] {
			t.Fatalf("applied out of order: %v", first.Applied)
		}
	}

	second, err := run(ctx, pool, migrations.FS)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(second.Applied) != 0 {
		t.Fatalf("second run applied %v, want nothing", second.Applied)
	}
	if len(second.Skipped) != len(first.Applied) {
		t.Fatalf("second run skipped %d, want %d", len(second.Skipped), len(first.Applied))
	}

	// And the bookkeeping agrees.
	var recorded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&recorded); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if recorded != len(first.Applied) {
		t.Fatalf("schema_migrations has %d rows, want %d", recorded, len(first.Applied))
	}
}

// --- 2. The application can actually use the schema the runner created ---

func TestRunProducesUsableSchema(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	if _, err := run(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("run: %v", err)
	}

	// The table and index the application depends on, checked unqualified so they resolve through
	// this test's search_path.
	var table *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('poems')::text`).Scan(&table); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	if table == nil {
		t.Fatal("poems table missing after migrations")
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO poems (id, content) VALUES ('22222222-2222-2222-2222-222222222222', 'x')`); err != nil {
		t.Fatalf("insert into migrated schema: %v", err)
	}

	var indexDef string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = current_schema() AND indexname = 'idx_poems_active_created_at'`).Scan(&indexDef); err != nil {
		t.Fatalf("index lookup: %v", err)
	}
	if !strings.Contains(indexDef, "deleted_at IS NULL") {
		t.Fatalf("unexpected index definition: %q", indexDef)
	}
}

// --- 3. A migration edited after being applied is refused ---

func TestRunRefusesModifiedMigration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	original := fstest.MapFS{
		"001_a.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t1 (id int);`)},
	}
	if _, err := run(ctx, pool, original); err != nil {
		t.Fatalf("initial run: %v", err)
	}

	edited := fstest.MapFS{
		"001_a.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t1 (id int, extra text);`)},
	}
	_, err := run(ctx, pool, edited)
	if err == nil {
		t.Fatal("run accepted a migration that had been edited after being applied")
	}
	if !strings.Contains(err.Error(), "modified after it was applied") {
		t.Fatalf("error does not name the problem: %v", err)
	}
}

// --- 4. A migration that no longer exists is refused ---

func TestRunRefusesVanishedMigration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	original := fstest.MapFS{
		"001_a.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t1 (id int);`)},
		"002_b.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t2 (id int);`)},
	}
	if _, err := run(ctx, pool, original); err != nil {
		t.Fatalf("initial run: %v", err)
	}

	// 002 renamed to 003, which is indistinguishable from deleting it and adding a new one.
	renamed := fstest.MapFS{
		"001_a.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t1 (id int);`)},
		"003_b.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t2 (id int);`)},
	}
	_, err := run(ctx, pool, renamed)
	if err == nil {
		t.Fatal("run accepted a migration that had been renamed away")
	}
	if !strings.Contains(err.Error(), "no longer exist") {
		t.Fatalf("error does not name the problem: %v", err)
	}
	if !strings.Contains(err.Error(), "002_b.sql") {
		t.Fatalf("error does not name the missing file: %v", err)
	}
}

// --- 5. A migration that fails leaves nothing behind ---

func TestRunRollsBackFailedMigrationAndDoesNotRecordIt(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	good := fstest.MapFS{
		"001_a.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t1 (id int);`)},
	}
	if _, err := run(ctx, pool, good); err != nil {
		t.Fatalf("initial run: %v", err)
	}

	// The first statement is valid and the second is not, so a runner without a per-file transaction
	// would leave t2 behind while recording nothing.
	broken := fstest.MapFS{
		"001_a.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t1 (id int);`)},
		"002_b.sql": &fstest.MapFile{Data: []byte(`
			CREATE TABLE IF NOT EXISTS t2 (id int);
			THIS IS NOT VALID SQL;
		`)},
	}
	if _, err := run(ctx, pool, broken); err == nil {
		t.Fatal("run accepted a migration containing invalid SQL")
	}

	var t2 *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('t2')::text`).Scan(&t2); err != nil {
		t.Fatalf("to_regclass: %v", err)
	}
	if t2 != nil {
		t.Fatal("t2 survived a failed migration; the file was not run in a transaction")
	}

	var recorded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE filename = '002_b.sql'`).Scan(&recorded); err != nil {
		t.Fatalf("count: %v", err)
	}
	if recorded != 0 {
		t.Fatal("a failed migration was recorded as applied")
	}

	// And it recovers: once the file is fixed, the same run applies it.
	fixed := fstest.MapFS{
		"001_a.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t1 (id int);`)},
		"002_b.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t2 (id int);`)},
	}
	result, err := run(ctx, pool, fixed)
	if err != nil {
		t.Fatalf("recovery run: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0] != "002_b.sql" {
		t.Fatalf("recovery applied %v, want exactly [002_b.sql]", result.Applied)
	}
}

// --- 6. A file may contain more than one statement ---

func TestRunSupportsMultiStatementMigration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	multi := fstest.MapFS{
		"001_multi.sql": &fstest.MapFile{Data: []byte(`
			CREATE TABLE IF NOT EXISTS m1 (id int);
			CREATE TABLE IF NOT EXISTS m2 (id int);
			CREATE INDEX IF NOT EXISTS m1_idx ON m1 (id);
		`)},
	}
	if _, err := run(ctx, pool, multi); err != nil {
		t.Fatalf("run rejected a multi-statement migration: %v", err)
	}

	for _, name := range []string{"m1", "m2"} {
		var rel *string
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1)::text`, name).Scan(&rel); err != nil {
			t.Fatalf("to_regclass(%s): %v", name, err)
		}
		if rel == nil {
			t.Fatalf("%s missing; later statements in the file were not executed", name)
		}
	}
}

// --- 7. A file with no SQL is handled, and the two flavors differ ---

// TestRunSkipsWhitespaceOnlyMigration covers a file that is empty once trimmed.
//
// A genuinely empty file is not a migration. Recording it would put a permanent bookkeeping row for
// a file that changes nothing, so it is skipped and never recorded. The important property is the
// second half: an unrecorded file that is also not applied stays that way, rather than appearing in
// schema_migrations and then being reported as missing on a later run.
func TestRunSkipsWhitespaceOnlyMigrationWithoutRecordingIt(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	set := fstest.MapFS{
		"001_a.sql":     &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t1 (id int);`)},
		"002_blank.sql": &fstest.MapFile{Data: []byte("   \n\n\t\n")},
	}
	result, err := run(ctx, pool, set)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0] != "001_a.sql" {
		t.Fatalf("applied %v, want exactly [001_a.sql]", result.Applied)
	}

	var recorded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE filename = '002_blank.sql'`).Scan(&recorded); err != nil {
		t.Fatalf("count: %v", err)
	}
	if recorded != 0 {
		t.Fatal("a blank migration was recorded as applied")
	}

	// Re-running must not trip the vanished-migration check on the file that was never applied.
	if _, err := run(ctx, pool, set); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

// TestRunAppliesCommentOnlyMigration pins the other half of that distinction.
//
// A file containing only comments is not blank, and Postgres accepts it as a valid empty statement
// batch. It is therefore applied and recorded like any other migration. That is the intended
// behavior: the file exists, it was processed, and recording it keeps the bookkeeping table an
// accurate description of the migration set.
func TestRunAppliesCommentOnlyMigration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	set := fstest.MapFS{
		"001_a.sql":    &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS t1 (id int);`)},
		"002_note.sql": &fstest.MapFile{Data: []byte("-- reserved for a future change\n")},
	}
	result, err := run(ctx, pool, set)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(result.Applied) != 2 {
		t.Fatalf("applied %v, want both files", result.Applied)
	}

	var recorded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE filename = '002_note.sql'`).Scan(&recorded); err != nil {
		t.Fatalf("count: %v", err)
	}
	if recorded != 1 {
		t.Fatal("a comment-only migration should be recorded as applied")
	}
}

// --- 8. The baseline case: a database created by the old boot DDL adopts the migrations ---

// TestRunAdoptsSchemaCreatedByBootDDL is the safety argument for running this against production.
//
// Every database that existed before this change was created by the server's own boot DDL and has
// never seen schema_migrations. Its poems table is already correct. The runner must therefore treat
// every migration as pending, apply them as no-ops, and record them, without error and without
// disturbing the data that is already there.
//
// If this test ever fails, the runner would either error on a real production database or, worse,
// report success while having done something to it.
func TestRunAdoptsSchemaCreatedByBootDDL(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// Exactly what the old EnsureSchema created, run directly.
	legacy := []string{
		`CREATE TABLE IF NOT EXISTS poems (
			id UUID PRIMARY KEY,
			content TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT now()
		)`,
		`ALTER TABLE poems ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP NULL`,
		`CREATE INDEX IF NOT EXISTS idx_poems_active_created_at
			ON poems (created_at DESC) WHERE deleted_at IS NULL`,
	}
	for _, stmt := range legacy {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("legacy DDL: %v", err)
		}
	}

	// A row that existed before the runner ever ran.
	const existing = "33333333-3333-3333-3333-333333333333"
	if _, err := pool.Exec(ctx,
		`INSERT INTO poems (id, content) VALUES ($1, 'written before the runner existed')`, existing); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	result, err := run(ctx, pool, migrations.FS)
	if err != nil {
		t.Fatalf("runner refused a legacy database: %v", err)
	}
	if len(result.Applied) == 0 {
		t.Fatal("runner adopted the legacy database without recording any migration")
	}

	// The pre-existing row survived.
	var content string
	if err := pool.QueryRow(ctx, `SELECT content FROM poems WHERE id = $1`, existing).Scan(&content); err != nil {
		t.Fatalf("pre-existing row was lost: %v", err)
	}
	if content != "written before the runner existed" {
		t.Fatalf("pre-existing row content = %q", content)
	}

	// And a second run is a no-op, which is what a deploy will actually do.
	second, err := run(ctx, pool, migrations.FS)
	if err != nil {
		t.Fatalf("second run on an adopted database: %v", err)
	}
	if len(second.Applied) != 0 {
		t.Fatalf("second run applied %v, want nothing", second.Applied)
	}
}

// --- 9. The shipped set is well formed ---

func TestEmbeddedMigrationsAreOrderedAndComplete(t *testing.T) {
	files, err := load(migrations.FS)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no embedded migrations")
	}

	seen := make(map[string]struct{}, len(files))
	for i, m := range files {
		if !strings.HasSuffix(m.name, ".sql") {
			t.Fatalf("non-SQL file embedded: %s", m.name)
		}
		if m.name != path.Base(m.name) {
			t.Fatalf("migration name is not a bare filename: %s", m.name)
		}
		if m.sql == "" {
			t.Fatalf("migration %s is empty after trimming", m.name)
		}
		if _, dup := seen[m.name]; dup {
			t.Fatalf("duplicate migration name: %s", m.name)
		}
		seen[m.name] = struct{}{}

		// The checksum must be over the trimmed body, so that reformatting a file's trailing
		// whitespace is the only change that does not trip the integrity check.
		if len(m.checksum) != 64 {
			t.Fatalf("migration %s has a %d-character checksum, want 64 hex characters", m.name, len(m.checksum))
		}
		if i > 0 && files[i-1].name >= m.name {
			t.Fatalf("migrations are not in filename order: %s then %s", files[i-1].name, m.name)
		}
	}
}

func TestRunRejectsNilPool(t *testing.T) {
	if _, err := run(context.Background(), nil, migrations.FS); err == nil {
		t.Fatal("run accepted a nil pool")
	}
}

// --- 10. Concurrent runs are serialized by the advisory lock ---

// TestRunSerializesConcurrentProcesses is the test that justifies the lock.
//
// Two application instances starting at once -- a rolling deploy, a scale-up, or simply the free
// instance being recycled while a request wakes it -- both reach the migration step together. Without
// the lock, both read an empty schema_migrations, both decide every migration is pending, and both
// apply them. The second would fail on a non-idempotent statement, or insert a duplicate bookkeeping
// row, and the failure would only ever appear under concurrent boot.
//
// The assertion is deliberately narrow. Both runs must succeed, and the union of what they applied
// must be exactly the migration set, applied once. It does not assert which one won, because that is
// a race with no correct answer.
func TestRunSerializesConcurrentProcesses(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	set := fstest.MapFS{
		"001_a.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS c1 (id int);`)},
		"002_b.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS c2 (id int);`)},
		"003_c.sql": &fstest.MapFile{Data: []byte(`CREATE TABLE IF NOT EXISTS c3 (id int);`)},
	}

	const racers = 4
	type outcome struct {
		result Result
		err    error
	}
	results := make(chan outcome, racers)

	start := make(chan struct{})
	for range racers {
		go func() {
			<-start // release all of them together, so they genuinely contend
			r, err := run(ctx, pool, set)
			results <- outcome{result: r, err: err}
		}()
	}
	close(start)

	applied := make([]string, 0, len(set))
	for i := range racers {
		out := <-results
		if out.err != nil {
			t.Fatalf("concurrent run %d failed: %v", i, out.err)
		}
		applied = append(applied, out.result.Applied...)
	}

	// Every migration was applied by exactly one racer.
	sort.Strings(applied)
	if len(applied) != len(set) {
		t.Fatalf("applied %v across %d racers, want each of %d exactly once", applied, racers, len(set))
	}
	for i, name := range applied {
		if i > 0 && applied[i-1] == name {
			t.Fatalf("migration %s was applied by more than one racer", name)
		}
	}

	// And the bookkeeping agrees, which is what a later run will trust.
	var recorded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&recorded); err != nil {
		t.Fatalf("count: %v", err)
	}
	if recorded != len(set) {
		t.Fatalf("schema_migrations has %d rows, want %d", recorded, len(set))
	}
}

// TestAcquireLockReportsBusyAndRecovers covers both halves of the bounded wait.
//
// A lock held by a process that will never release it has to produce an explanatory error rather than
// a boot that appears to hang. And when the holder does release, the waiter must succeed rather than
// staying blocked, which is the ordinary case of one instance waiting on another.
//
// The deadline is passed explicitly, so this runs in milliseconds instead of waiting out the real
// lockWait that production uses.
func TestAcquireLockReportsBusyAndRecovers(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// A key of this test's own, not the production one. pg advisory locks are database-wide, so
	// competing for migrationLockKey here would make this test fail whenever any other package ran a
	// migration concurrently -- which is exactly what happened once -p 1 stopped serializing the
	// packages. The lock's behavior is what is under test, not which key holds it.
	const key int64 = 0x7E57_0BAD_C0DE_0001

	hogger, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the hogging connection: %v", err)
	}
	defer hogger.Release()

	var held bool
	if err := hogger.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&held); err != nil {
		t.Fatalf("take the lock: %v", err)
	}
	if !held {
		t.Fatal("could not take the lock; a previous test leaked it")
	}

	// A second connection, so the try-lock below genuinely contends rather than being handed the
	// same session back.
	waiter, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the waiting connection: %v", err)
	}
	defer waiter.Release()

	if _, err := acquireLock(ctx, waiter, key, 200*time.Millisecond); err == nil {
		t.Fatal("acquireLock succeeded while another connection held the lock")
	} else if !strings.Contains(err.Error(), "busy") {
		t.Fatalf("error does not explain that the database is busy: %v", err)
	}

	// A waiter that gave up must not have left a hold behind. pg_try_advisory_lock is re-entrant for
	// a session that already holds the lock, so re-probing the hogger would succeed either way and
	// prove nothing. Counting the advisory locks in the cluster is the direct observation: only the
	// hogger should hold one.
	if got := advisoryLockHolders(t, pool, key); got != 1 {
		t.Fatalf("%d sessions hold an advisory lock, want 1; a failed acquire leaked a hold", got)
	}

	// Now let go, and the waiter must get in.
	if _, err := hogger.Exec(ctx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
		t.Fatalf("release the lock: %v", err)
	}
	if got := advisoryLockHolders(t, pool, key); got != 0 {
		t.Fatalf("%d advisory locks held after the release, want 0", got)
	}

	unlock, err := acquireLock(ctx, waiter, key, 5*time.Second)
	if err != nil {
		t.Fatalf("acquireLock did not recover after the holder released: %v", err)
	}
	unlock()
}

// advisoryLockHolders counts the sessions currently holding a specific advisory lock key.
//
// Scoped to one key deliberately. Postgres advisory locks live in a database-wide namespace, so
// counting all of them attributes another package's legitimate lock to this test -- which is what
// happened, since a concurrent migrate.Run in another package holds the production key. That first
// version counted every lock and failed for reasons that had nothing to do with the code under test.
//
// A bigint key is reported by Postgres as classid (high 32 bits), objid (low 32) and objsubid 1.
func advisoryLockHolders(t *testing.T, pool *pgxpool.Pool, key int64) int {
	t.Helper()

	// Both sides cast to bigint. classid and objid are unsigned oids, so casting the computed halves
	// to a signed int raises "integer out of range" for any key whose low half exceeds 2^31 -- which
	// is most of them. Widening both sides sidesteps the signedness question entirely.
	const query = `
		SELECT count(*)
		FROM pg_locks
		WHERE locktype = 'advisory'
		  AND classid::bigint = (($1::bigint >> 32) & 0xFFFFFFFF)
		  AND objid::bigint = ($1::bigint & 0xFFFFFFFF)
		  AND objsubid = 1`

	var count int
	if err := pool.QueryRow(context.Background(), query, key).Scan(&count); err != nil {
		t.Fatalf("count advisory locks: %v", err)
	}
	return count
}

// TestMigrationBoundsAreScopedToTheRun pins both halves of the contract the migration timeouts have
// to satisfy at once: they are in force while the run is executing, and they are gone from the
// connection afterwards.
//
// Both halves are asserted through run() rather than by calling sessionBounds directly, because the
// interesting half is the one that only the caller can get right. set_config is session-scoped, and
// the session is a pooled connection the application will hand out again, so a runner that set the
// bounds and forgot to clear them would pass a test of the helper while leaving every subsequent
// request on that connection capped at 60s. That is a failure that appears under load and points
// nowhere near the migration that caused it.
//
// The settings are observed from inside a migration rather than from a connection held open beside
// it, so what is asserted is what a real migration would actually run under.
func TestMigrationBoundsAreScopedToTheRun(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// The probe migration records the settings in force at the moment its own statements run.
	probe := fstest.MapFS{
		"001_probe.sql": &fstest.MapFile{Data: []byte(`
			CREATE TABLE bounds_seen (
				lock      interval NOT NULL,
				statement interval NOT NULL
			);
			INSERT INTO bounds_seen
			SELECT current_setting('lock_timeout')::interval,
			       current_setting('statement_timeout')::interval;
		`)},
	}
	if _, err := run(ctx, pool, probe); err != nil {
		t.Fatalf("run: %v", err)
	}

	// In force during the run, at exactly the shipped values.
	var gotLock, gotStatement time.Duration
	const readSeen = `SELECT lock, statement FROM bounds_seen`
	if err := pool.QueryRow(ctx, readSeen).Scan(&gotLock, &gotStatement); err != nil {
		t.Fatalf("read the observed bounds: %v", err)
	}
	if gotLock != tableLockTimeout {
		t.Fatalf("lock_timeout in force was %v, want the shipped %v", gotLock, tableLockTimeout)
	}
	if gotStatement != statementTimeout {
		t.Fatalf("statement_timeout in force was %v, want the shipped %v", gotStatement, statementTimeout)
	}

	// And gone afterwards. The default for both is 0, meaning disabled: wait as long as it takes.
	//
	// Every connection is checked, not just one. The pool may hand back the session that migrated or
	// a different one, and a single sample would pass even if the bounds had been left on whichever
	// connection the test happened to reacquire.
	conns := pool.AcquireAllIdle(ctx)
	if len(conns) == 0 {
		t.Fatal("no idle connections to check; the pool returned none after a run")
	}
	defer func() {
		for _, c := range conns {
			c.Release()
		}
	}()

	for i, c := range conns {
		for _, setting := range []string{"lock_timeout", "statement_timeout"} {
			var got float64
			// current_setting normalises for display -- 60s comes back as '1min' -- so the value is
			// converted to seconds and compared numerically. Comparing the text would make this test
			// depend on Postgres's duration formatting rather than on the value actually in force.
			const read = `SELECT EXTRACT(EPOCH FROM current_setting($1)::interval)`
			if err := c.QueryRow(ctx, read, setting).Scan(&got); err != nil {
				t.Fatalf("read %s on connection %d: %v", setting, i, err)
			}
			if got != 0 {
				t.Fatalf("%s leaked onto pooled connection %d as %vs, want the 0 (disabled) default",
					setting, i, got)
			}
		}
	}
}

// TestRunFailsFastWhenAMigrationNeedsALockedTable is the case that motivated the bounds at all.
//
// An ALTER TABLE needs ACCESS EXCLUSIVE, and an ordinary request holds a weaker lock on the same
// table for the length of its own query. The host performs zero-downtime deploys, so the outgoing
// instance is still querying while the incoming one migrates. Unbounded, that ALTER waits inside
// Postgres with no Go context and no output until the platform kills the process -- which surfaces
// as an unrelated crash and can leave a half-rewritten table.
//
// This asserts three things: the run fails, it fails *naming the file* so the operator knows which
// statement to retry, and it fails within a bound rather than never. The real tableLockTimeout is
// used rather than a test-sized one on purpose: a shortened timeout would prove the mechanism works
// without proving the shipped value is the one that rescues a deploy.
func TestRunFailsFastWhenAMigrationNeedsALockedTable(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	const create = `
		CREATE TABLE IF NOT EXISTS contended (
			id int,
			stamp timestamp
		)`
	if _, err := pool.Exec(ctx, create); err != nil {
		t.Fatalf("create the contended table: %v", err)
	}

	// A separate connection holds ACCESS EXCLUSIVE, standing in for a long-running request against
	// the live table. Released only after the assertion, so the lock is genuinely held throughout.
	//
	// The lock has to be taken inside an open transaction, because LOCK TABLE is only valid there
	// and the lock lasts exactly as long as the transaction does. Rolling back rather than committing
	// is deliberate: there is nothing to persist, and it keeps the table untouched.
	hogger, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the hogging connection: %v", err)
	}
	defer hogger.Release()
	holder, err := hogger.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the locking transaction: %v", err)
	}
	defer func() { discardHolderRollback(holder.Rollback(ctx)) }()
	if _, err := holder.Exec(ctx, `LOCK TABLE contended IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("take the lock: %v", err)
	}

	alter := fstest.MapFS{
		"001_alter.sql": &fstest.MapFile{Data: []byte(`ALTER TABLE contended ALTER COLUMN stamp TYPE timestamptz;`)},
	}

	start := time.Now()
	_, runErr := run(ctx, pool, alter)
	elapsed := time.Since(start)

	if runErr == nil {
		t.Fatal("run succeeded while another session held ACCESS EXCLUSIVE on the table; the lock bound is not in force")
	}
	// The file name is the whole point: an operator reading a failed deploy needs to know which
	// statement to retry, and apply() is what supplies it.
	if !strings.Contains(runErr.Error(), "001_alter.sql") {
		t.Fatalf("error does not name the migration file: %v", runErr)
	}
	// A generous ceiling rather than a tight one. The point is that it returned at all: with no
	// bound this call does not return within the life of the test binary. Allow for a slow CI
	// machine and the 5s the timeout itself costs, and fail only on the open-ended case.
	if ceiling := 4 * tableLockTimeout; elapsed > ceiling {
		t.Fatalf("run took %v, want it to give up within %v of failing on a held lock", elapsed, ceiling)
	}

	// And the failure must have left nothing behind. A migration that raised a lock timeout should
	// not be recorded, or the next deploy would skip the very statement that never ran.
	var recorded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE filename = '001_alter.sql'`).Scan(&recorded); err != nil {
		t.Fatalf("count applied: %v", err)
	}
	if recorded != 0 {
		t.Fatal("a migration that could not take its lock was recorded as applied; the next deploy would skip it")
	}
}

// discardHolderRollback consumes a Rollback error for the reasons given at its only call site.
//
// errcheck runs with check-blank, so an error assigned to the blank identifier is still reported.
// Passing it to a function that returns nothing is the way to say "deliberately ignored" in a form
// the linter accepts -- the same reasoning as discardRollback in the runner.
func discardHolderRollback(error) {}

// TestTimestampMigrationIsZoneIndependent is the test that earns the USING clause in 006.
//
// A bare `ALTER COLUMN ... TYPE TIMESTAMPTZ` reinterprets zone-less values in the *session's* zone,
// so the same file would move every poem by a different amount depending on the host it ran on. This
// runs the real migration against the real prior schema with the session deliberately set away from
// UTC, and asserts the instants are untouched.
//
// The counterfactual at the end is not decoration. It applies the obvious bare form to an identical
// table and asserts that it *does* shift the value, which pins down both that this test can detect a
// regression and why the clause is there -- a test that only proves the good path cannot tell a
// correct conversion from a correct-looking coincidence.
func TestTimestampMigrationIsZoneIndependent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// The real 001-005, so the pre-migration shape is the one production actually has rather than a
	// hand-written approximation that could drift from it.
	all, err := load(migrations.FS)
	if err != nil {
		t.Fatalf("load the embedded migrations: %v", err)
	}
	prior := fstest.MapFS{}
	var normalize migration
	for _, m := range all {
		if m.name == "006_timestamptz.sql" {
			normalize = m
			continue
		}
		prior[m.name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	if normalize.name == "" {
		t.Fatal("006_timestamptz.sql is not embedded; the migration this test verifies is missing")
	}
	if _, err := run(ctx, pool, prior); err != nil {
		t.Fatalf("apply 001-005: %v", err)
	}

	// A zone-less value, written the way the column was actually populated: now() cast into a
	// TIMESTAMP by a database running in UTC.
	const wrote = "2024-01-01 00:00:00"
	const poemID = "11111111-1111-1111-1111-111111111111"
	if _, err := pool.Exec(ctx,
		`INSERT INTO poems (id, content, created_at) VALUES ($1, $2, $3::timestamp)`,
		poemID, "the work itself", wrote); err != nil {
		t.Fatalf("insert the poem: %v", err)
	}

	// The session that runs the migration, moved away from UTC. This is the whole test: everything
	// below happens with a non-UTC clock in force.
	migrator, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the migrating connection: %v", err)
	}
	defer migrator.Release()
	if _, err := migrator.Exec(ctx, `SET TIME ZONE 'Asia/Kolkata'`); err != nil {
		t.Fatalf("shift the session zone: %v", err)
	}
	if _, err := migrator.Exec(ctx, normalize.sql); err != nil {
		t.Fatalf("apply 006 under a non-UTC session: %v", err)
	}

	// The types moved, and the instant did not. Equal compares instants rather than wall clocks,
	// which is the only comparison that means anything across a zone change.
	for _, tc := range []struct{ table, column string }{
		{"poems", "created_at"},
		{"poems", "deleted_at"},
		{"poem_versions", "recorded_at"},
	} {
		var gotType string
		const readType = `
			SELECT format_type(a.atttypid, a.atttypmod)
			FROM pg_attribute a
			JOIN pg_class c ON c.oid = a.attrelid
			WHERE c.relname = $1 AND a.attname = $2 AND a.attnum > 0`
		if err := migrator.QueryRow(ctx, readType, tc.table, tc.column).Scan(&gotType); err != nil {
			t.Fatalf("read the type of %s.%s: %v", tc.table, tc.column, err)
		}
		if gotType != "timestamp with time zone" {
			t.Fatalf("%s.%s is %q, want timestamp with time zone", tc.table, tc.column, gotType)
		}
	}

	var gotCreated time.Time
	if err := migrator.QueryRow(ctx, `SELECT created_at FROM poems WHERE id = $1`, poemID).Scan(&gotCreated); err != nil {
		t.Fatalf("read created_at: %v", err)
	}
	want, err := time.Parse(time.RFC3339, "2024-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("parse the expected instant: %v", err)
	}
	if !gotCreated.Equal(want) {
		t.Fatalf("created_at is %v, want the instant %v; the conversion shifted it",
			gotCreated.UTC(), want.UTC())
	}

	// The counterfactual: the obvious bare form, on an identical column in the same session.
	//
	// Two separate Exec calls rather than one multi-statement string. A pool connection defaults to
	// the extended protocol, which permits exactly one statement per execution -- the same limitation
	// apply() works around with QueryExecModeSimpleProtocol, and worth respecting here too.
	if _, err := migrator.Exec(ctx, `CREATE TABLE bare_form (stamp timestamp)`); err != nil {
		t.Fatalf("create the counterfactual table: %v", err)
	}
	if _, err := migrator.Exec(ctx, `INSERT INTO bare_form (stamp) VALUES ($1::timestamp)`, wrote); err != nil {
		t.Fatalf("populate the counterfactual table: %v", err)
	}
	if _, err := migrator.Exec(ctx, `ALTER TABLE bare_form ALTER COLUMN stamp TYPE TIMESTAMPTZ`); err != nil {
		t.Fatalf("apply the bare form: %v", err)
	}
	var bare time.Time
	if err := migrator.QueryRow(ctx, `SELECT stamp FROM bare_form`).Scan(&bare); err != nil {
		t.Fatalf("read the counterfactual: %v", err)
	}
	if bare.Equal(want) {
		t.Fatal("the bare form did not shift the value under this session zone, so this test cannot " +
			"detect the regression it exists to prevent; check the session TimeZone is still non-UTC")
	}
	t.Logf("bare form shifted %v to %v; the USING clause holds it at %v",
		want.UTC().Format(time.RFC3339), bare.UTC().Format(time.RFC3339), gotCreated.UTC().Format(time.RFC3339))
}
