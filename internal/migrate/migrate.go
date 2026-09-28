// Package migrate applies the SQL migrations in the migrations package to a database.
//
// It replaces the previous arrangement, in which the server created its own schema on boot with DDL
// hardcoded in Go while a separate migrations directory held a second, equivalent, and entirely
// independent copy of the same schema. Two sources of truth for one schema meant an edit to the
// obvious one -- a .sql file -- silently had no effect at runtime.
//
// The runner is the only thing that creates schema. The server no longer holds DDL privileges.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/migrations"
)

// bookkeepingDDL creates the record of what has been applied.
//
// The table is created with IF NOT EXISTS so that the runner is safe against a database that has
// been managed by hand, or by a version of the application that created its schema on boot, and has
// therefore never seen this table. That is the expected state for every database that existed before
// this runner, and it is why the column definitions here must never change incompatibly.
const bookkeepingDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	filename   TEXT PRIMARY KEY,
	checksum   TEXT NOT NULL,
	applied_at TIMESTAMP NOT NULL DEFAULT now()
)`

// discardRollback consumes a Rollback error for the reasons given at its only call site.
//
// errcheck runs with check-blank, so an error assigned to the blank identifier is still reported.
// Passing it to a function that returns nothing is the way to say "deliberately ignored" in a form
// the linter accepts.
func discardRollback(error) {}

// migrationLockKey identifies the advisory lock that serializes migration runs across processes.
//
// Arbitrary but fixed forever. It only has to be consistent between instances of this application;
// there is no other software taking it.
//
// The value spells "versemg" in ASCII, so a session sitting in pg_locks can be traced back to this
// application by eye. It fits in an int64, which is the type pg_advisory_lock takes.
const migrationLockKey int64 = 0x76657273656D67

// lockWait bounds how long a run waits for another process to finish migrating.
//
// Without a bound, a process holding the lock and never releasing it -- a crashed instance whose
// session has not yet been reaped, say -- would leave the new instance waiting inside Postgres with
// no output, until the platform killed it. Failing with a message naming the cause is more useful
// than a boot that appears to hang.
const lockWait = 30 * time.Second

// lockPollInterval is how often a waiting run retries. Short enough that the common case of one
// process waiting on another resolves promptly, long enough not to hammer the database.
const lockPollInterval = 250 * time.Millisecond

// tableLockTimeout bounds how long a migration waits for a conflicting lock on a table.
//
// lockWait above bounds migration runs against each other. It does nothing for the other direction,
// and that is the direction that takes down a deploy. An ALTER TABLE needs ACCESS EXCLUSIVE on the
// table it rewrites, while an ordinary request holds a weaker lock on that same table for the
// length of its own query. The platform performs zero-downtime deploys, so the outgoing instance
// is still serving -- and still querying -- while the incoming one migrates, and the two overlap by
// design rather than by accident.
//
// Without a bound the ALTER waits inside Postgres holding no Go context and printing nothing, until
// the platform kills the process. That is the worst available outcome: the failure surfaces as an
// unrelated crash, and a migration interrupted mid-flight can leave a half-rewritten table. Failing
// here instead, naming the file, is recoverable -- the next deploy retries against a quiet database.
const tableLockTimeout = 5 * time.Second

// statementTimeout bounds a single migration statement.
//
// Distinct from tableLockTimeout, which bounds waiting for a lock. This one bounds execution once
// the statement already holds what it needs, so it catches the other way a run can hang: a query
// that acquires its locks promptly and then never finishes.
//
// Deliberately not configurable from the environment, unlike the pool settings. A migration that
// behaved differently depending on which instance happened to run it is a worse failure than a
// uniform one. If a future migration legitimately needs longer -- a bulk backfill, say -- raise it
// here, visibly, in the same commit as that migration.
const statementTimeout = 60 * time.Second

// RunBudget bounds a whole migration run, and is the deadline callers should put on the context
// they pass to Run.
//
// tableLockTimeout and statementTimeout are both enforced by the server, so neither of them helps
// when the client is the side that has stopped hearing back -- a connection that is established but
// silent, a network that has gone away without a FIN. There the server is not in a position to
// enforce anything, and the run would wait forever. A client-side deadline covers that case.
//
// Generous enough for a first run against a cold free-tier database, where every file is applied
// rather than skipped, and short enough that a stuck boot fails with a message instead of being
// left to the platform's own timeout.
const RunBudget = 5 * time.Minute

// migration is one SQL file, with the identity and integrity value derived from its contents.
type migration struct {
	name     string
	sql      string
	checksum string
}

// appliedRecord is a row in schema_migrations.
type appliedRecord struct {
	name     string
	checksum string
}

// Result summarizes what a run did, so callers can report it and tests can assert on it.
type Result struct {
	Applied []string
	Skipped []string
}

// Run applies every migration that has not yet been applied, in filename order, and returns a summary.
//
// Three properties matter more than convenience here:
//
//   - Each file is applied in its own transaction, and the bookkeeping row is written in that same
//     transaction. A migration that fails halfway therefore leaves neither partial schema nor a
//     false record of success, and re-running resumes from that file rather than skipping past it.
//   - A file already applied is not re-applied. The previous runner re-applied everything on every
//     invocation, which was only safe because every statement happened to be IF NOT EXISTS.
//   - A file already applied whose contents have changed is an error, as is a recorded file that no
//     longer exists. Both mean the schema on disk and the schema in the database have diverged, and
//     continuing would apply one history on top of another.
//
// The caller must hold a credential permitted to create and alter tables. The application applies
// migrations at startup rather than as a separate deploy step, so that credential is the runtime
// credential. See docs/RUNNING.md for why, and for what would change it.
func Run(ctx context.Context, pool *pgxpool.Pool) (Result, error) {
	return run(ctx, pool, migrations.FS)
}

// run is Run with an explicit migration source.
//
// The embedded set is what ships. This indirection exists so the tests can present a migration set
// that misbehaves -- a file edited after it was applied, a file that no longer exists, a file that
// fails partway through. Those are the cases that justify the bookkeeping, and none of them can be
// provoked through the embedded set without editing the repository.
//
// The order of the steps below is the whole design. The applied set is read *after* the lock is
// taken, never before. Reading it first and locking afterwards is the bug this arrangement exists to
// prevent: two processes starting at once would both read an empty table, both conclude that every
// migration is pending, and both apply them. The second would then fail on a non-idempotent
// statement, or duplicate a row that a future runner would find inconsistent. Taking the lock first
// makes the second process wait, and re-reading after the wait is what lets it see the work the first
// one already did.
//
// For the same reason the lock is held on one dedicated connection for the whole run rather than
// taken and released per statement: a Postgres advisory lock belongs to the session that took it, so
// a lock taken on one pooled connection protects nothing on another.
func run(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (Result, error) {
	var result Result

	if pool == nil {
		return result, errors.New("database pool is nil")
	}

	files, err := load(fsys)
	if err != nil {
		return result, err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return result, fmt.Errorf("acquire a connection to migrate with: %w", err)
	}
	defer conn.Release()

	// Before the advisory lock: every statement this run issues from here on is covered by the
	// bounds, including the bookkeeping DDL below.
	if err := sessionBounds(ctx, conn, tableLockTimeout, statementTimeout); err != nil {
		return result, err
	}
	// Registered after the release above, so it runs before it: the connection must go back to the
	// pool without this run's timeouts still attached to it.
	defer func() { discardReset(resetSessionBounds(context.WithoutCancel(ctx), conn)) }()

	unlock, err := acquireLock(ctx, conn, migrationLockKey, lockWait)
	if err != nil {
		return result, err
	}
	defer unlock()

	// Everything below runs while the lock is held, including the bookkeeping table itself: two
	// processes creating it concurrently is exactly the kind of race the lock exists to remove.
	if _, err := conn.Exec(ctx, bookkeepingDDL); err != nil {
		return result, fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := loadApplied(ctx, conn)
	if err != nil {
		return result, err
	}

	if err := checkNoVanished(applied, files); err != nil {
		return result, err
	}

	for _, m := range files {
		record, isApplied := applied[m.name]
		if isApplied {
			if record.checksum != m.checksum {
				return result, fmt.Errorf(
					"migration %s was modified after it was applied (recorded checksum %s, file checksum %s); "+
						"restore the original file, or write a new migration instead of editing an applied one",
					m.name, record.checksum, m.checksum)
			}
			result.Skipped = append(result.Skipped, m.name)
			continue
		}

		if err := apply(ctx, conn, m); err != nil {
			return result, err
		}
		result.Applied = append(result.Applied, m.name)
	}

	return result, nil
}

// sessionBounds bounds the two ways a migration can block without end: waiting for a lock, and
// executing without finishing.
//
// It is called on the acquired connection before the advisory lock is taken, so that every
// statement the run issues afterwards is covered -- including the bookkeeping DDL, which is why it
// belongs here rather than further down. SET is session-scoped and the runner holds one session for
// the whole run, so these bounds cannot leak to whatever borrows the connection afterwards.
//
// set_config is used rather than a literal `SET lock_timeout = ...` because Postgres does not accept
// a bind parameter in a SET statement, and interpolating a duration into SQL text is exactly the
// kind of thing that goes wrong quietly.
func sessionBounds(ctx context.Context, conn *pgxpool.Conn, tableLock, statement time.Duration) error {
	const setBounds = `SELECT set_config('lock_timeout', $1, false), set_config('statement_timeout', $2, false)`
	if _, err := conn.Exec(ctx, setBounds, pgSeconds(tableLock), pgSeconds(statement)); err != nil {
		return fmt.Errorf("bound the migration session: %w", err)
	}
	return nil
}

// pgSeconds renders a duration as a Postgres interval literal.
//
// Always a whole number of seconds rather than Go's own formatting. time.Duration.String emits
// forms such as "1m0s" and "500ms" that Postgres happens to accept but that are not worth
// depending on; a count of seconds is unambiguous, and both bounds are whole seconds by design.
func pgSeconds(d time.Duration) string {
	return strconv.Itoa(int(d.Seconds())) + "s"
}

// resetSessionBounds returns both timeouts to the server default, which is 0 for each: disabled,
// meaning wait as long as it takes.
//
// This is not tidiness. set_config is session-scoped, and the session is a pooled connection that the
// application will hand out again afterwards. Without the reset, a connection that once ran a
// migration would carry this release's 60s statement timeout into ordinary requests for the rest of
// its life, turning a migration safeguard into a runtime failure that appears under load and points
// nowhere near the migration that caused it. The test for this is
// TestSessionBoundsAreSetOnTheMigratingSessionOnly, which is what found it.
//
// The context is not the run's: this is called while unwinding, and the original may already be
// canceled, which would abort the very cleanup that makes the connection safe to reuse.
func resetSessionBounds(ctx context.Context, conn *pgxpool.Conn) error {
	const resetBounds = `SELECT set_config('lock_timeout', $1, false), set_config('statement_timeout', $1, false)`
	if _, err := conn.Exec(ctx, resetBounds, "0"); err != nil {
		return fmt.Errorf("restore the migration session's timeouts: %w", err)
	}
	return nil
}

// discardReset consumes a reset error for the reasons given at its only call site.
func discardReset(error) {}

// acquireLock takes the migration advisory lock, waiting up to wait, and returns the function that
// releases it.
//
// The key and the deadline are both parameters rather than constants, for the same reason. A test
// that exercised this against the production key would be competing for a database-wide lock with
// every other package's migration run: pg advisory locks are scoped to the database, not to a schema,
// so two packages migrating their own schemas still contend for the same one. That collision was
// invisible while CI serialized packages with -p 1, and surfaced as an intermittent failure the
// moment it did not. Taking the key as an argument lets a test use its own and still observe the
// exhausted-wait path, without that dependency.
func acquireLock(ctx context.Context, conn *pgxpool.Conn, key int64, wait time.Duration) (func(), error) {
	deadline := time.Now().Add(wait)

	for {
		var acquired bool
		// pg_try_advisory_lock returns a boolean rather than raising, so this needs no error
		// handling beyond a transport failure.
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired); err != nil {
			return nil, fmt.Errorf("try to take the migration lock: %w", err)
		}
		if acquired {
			return func() {
				// WithoutCancel: the release must happen even when the run is unwinding because its
				// context was canceled. Otherwise the session goes back to the pool still holding
				// the lock, and every later run on that pool blocks until lockWait expires.
				if _, err := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, key); err != nil {
					discardUnlock(err)
				}
			}, nil
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"another process has held the migration lock for more than %s; "+
					"migrations are not being applied because the database is busy, not because this "+
					"instance cannot reach it", wait)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

// discardUnlock consumes an unlock error.
//
// A failure here means the session is already gone, in which case Postgres has released the lock
// with it and there is nothing left to do. That is the only way it can fail once the lock is held, and
// it is reported nowhere useful -- the run itself already succeeded or failed on its own merits.
func discardUnlock(error) {}

// apply runs one migration and records it, atomically.
//
// The migration is executed in the simple protocol so that a file may contain more than one
// statement. The default exec mode prepares a statement, and the extended protocol allows only one
// statement per execution, which would make any future multi-statement migration fail in a way that
// looks like a SQL error rather than a runner limitation.
func apply(ctx context.Context, conn *pgxpool.Conn, m migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction for %s: %w", m.name, err)
	}
	// Unconditional: after a successful Commit this returns pgx.ErrTxClosed, and a Rollback issued
	// while another error is already propagating cannot be acted on, because the caller returns that
	// earlier error, which is the one worth reporting.
	//
	// Wrapped in a closure on purpose. A deferred call evaluates its arguments when the defer
	// statement is reached, not when the function returns, so `defer discardRollback(tx.Rollback(ctx))`
	// would roll the transaction back immediately and the migration would then fail with "tx is
	// closed" on its second statement.
	defer func() { discardRollback(tx.Rollback(ctx)) }()

	if _, err := tx.Conn().Exec(ctx, m.sql, pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.name, err)
	}

	// Recorded inside the same transaction as the DDL. If this insert failed on its own, the
	// migration would look unapplied while its schema changes were already committed.
	const record = `INSERT INTO schema_migrations (filename, checksum) VALUES ($1, $2)`
	if _, err := tx.Exec(ctx, record, m.name, m.checksum); err != nil {
		return fmt.Errorf("record migration %s: %w", m.name, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.name, err)
	}

	return nil
}

// load reads the embedded migrations in filename order.
//
// Order is by filename because the filenames are zero-padded sequence numbers, and a lexicographic
// sort is therefore also a numeric one. Sorting on anything else -- modification time, for instance
// -- would make the applied order depend on how the files were checked out.
func load(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	files := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		content, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		body := strings.TrimSpace(string(content))
		if body == "" {
			// An empty file is a no-op rather than an error, matching the previous runner. The one
			// thing it must never be is silently recorded as applied, so it is skipped outright and
			// never reaches the bookkeeping table.
			continue
		}

		sum := sha256.Sum256([]byte(body))
		files = append(files, migration{
			name:     entry.Name(),
			sql:      body,
			checksum: hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	return files, nil
}

// loadApplied reads the bookkeeping table.
func loadApplied(ctx context.Context, conn *pgxpool.Conn) (map[string]appliedRecord, error) {
	rows, err := conn.Query(ctx, `SELECT filename, checksum FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]appliedRecord)
	for rows.Next() {
		var record appliedRecord
		if err := rows.Scan(&record.name, &record.checksum); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[record.name] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}

	return applied, nil
}

// checkNoVanished reports migrations recorded in the database that no longer exist as files.
//
// Renaming a migration looks like deleting one and creating another: the database remembers the old
// name forever. Without this check the runner would happily apply the new file on top of a schema
// that the old one already changed, which is precisely the history rewrite that makes a schema
// impossible to reason about later.
func checkNoVanished(applied map[string]appliedRecord, files []migration) error {
	present := make(map[string]struct{}, len(files))
	for _, m := range files {
		present[m.name] = struct{}{}
	}

	missing := make([]string, 0)
	for name := range applied {
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	sort.Strings(missing)

	return fmt.Errorf(
		"these migrations are recorded as applied but no longer exist: %s; "+
			"a migration that has been applied must never be deleted or renamed, because the database "+
			"already reflects it -- restore the file, or write a new migration that supersedes it",
		strings.Join(missing, ", "))
}
