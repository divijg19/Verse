package database

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is the global database connection pool.
var Pool *pgxpool.Pool

// ErrNotInitialized is returned when a query is attempted before Connect has succeeded.
var ErrNotInitialized = errors.New("database not initialized")

// Require reports whether the pool is ready to be queried, returning ErrNotInitialized if not.
//
// Every read and write in internal/services and internal/export began with the same three lines:
//
//	if database.Pool == nil {
//	    return nil, fmt.Errorf("database not initialized")
//	}
//
// Eighteen copies of that is eighteen places to keep the message identical, and the message is the
// only thing a user sees when a deploy races its own migrations. This makes it one place, and gives
// the condition a name so callers can test for it with errors.Is rather than by matching a string.
//
// It exists because the failure is reachable in a running process and not only in a broken one:
// /health is registered before the pool is connected, and the tests swap the pool to measure it. A
// nil dereference in either case is a panic that reads as a crash rather than as "not ready yet".
func Require() error {
	if Pool == nil {
		return ErrNotInitialized
	}
	return nil
}

// Connect initializes the global pgxpool using DATABASE_URL.
// It returns an error if DATABASE_URL is missing or the pool cannot be created/pinged.
func Connect() error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL environment variable not set")
	}

	// Retry strategy for transient sleep/wakeup (e.g., Neon free tier)
	const attempts = 10
	const delay = 2 * time.Second

	var lastErr error
	for i := 1; i <= attempts; i++ {
		// Per-attempt context to bound pool creation and ping
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			cancel()
			return fmt.Errorf("failed to parse DATABASE_URL: %w", err)
		}

		// Re-plan rather than reuse a server-side prepared statement.
		//
		// The default mode caches named prepared statements, and Postgres refuses to re-execute one
		// whose result type has since changed: "cached plan must not change result type" (SQLSTATE
		// 0A000). That is not a theoretical hazard here, it is a direct consequence of migration 006
		// and of how this application deploys.
		//
		// The host performs zero-downtime deploys, so the outgoing instance is still serving while
		// the incoming one migrates. It has already prepared its statements. The incoming instance
		// rewrites poems.created_at, poems.deleted_at and poem_versions.recorded_at, and every query
		// selecting those columns in the outgoing instance -- the library, the poem view, the export,
		// the dashboard, the recycle, the history -- now fails with 0A000 on its cached plan, for as
		// long as that instance lives. A user pressing "export" during a deploy would get a 500.
		//
		// CacheDescribe keeps the description cache, so the common case still avoids a round trip, but
		// re-plans the statement on each execution instead of executing a stale one. The cost is
		// re-planning work Postgres would otherwise skip, on a pool of five connections serving one
		// author; the alternative is a deploy-time failure whose cause is nowhere near its symptom.
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe

		// Apply sensible defaults for Neon/free-tier
		if v := os.Getenv("DB_MAX_CONNS"); v != "" {
			// Bounded explicitly: a narrowing conversion from a parsed int wraps silently on a
			// hostile or careless value, and a negative pool size is a runtime panic.
			// #nosec G109 -- the bound immediately above is the mitigation. gosec tracks the value
			// back to strconv.Atoi and reports the narrowing conversion without evaluating the
			// guard, so the fix is invisible to it. math.MaxInt32 is checked, so the conversion
			// cannot wrap.
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= math.MaxInt32 {
				cfg.MaxConns = int32(n) // #nosec G109
			}
		} else {
			cfg.MaxConns = 5
		}
		if v := os.Getenv("DB_MIN_CONNS"); v != "" {
			// #nosec G109 -- see the note on DB_MAX_CONNS above; the guard is the mitigation.
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= math.MaxInt32 {
				cfg.MinConns = int32(n) // #nosec G109
			}
		} else {
			cfg.MinConns = 1
		}

		if v := os.Getenv("DB_MAX_CONN_LIFETIME"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				cfg.MaxConnLifetime = d
			}
		} else {
			cfg.MaxConnLifetime = time.Hour
		}

		// Idle time should be small to avoid exhausted free-tier connections
		if v := os.Getenv("DB_MAX_CONN_IDLE"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				cfg.MaxConnIdleTime = d
			}
		} else {
			cfg.MaxConnIdleTime = 5 * time.Minute
		}

		// Pin every session to UTC, on every connection the pool opens.
		//
		// pgx does not read the TZ environment variable; only Postgres's own PGTZ sets the session
		// zone, and that is a server-side setting no Go process can rely on being present. What
		// configures the session is whatever the database server was started or configured with, so
		// the same binary reads dates differently on two hosts with the same data.
		//
		// That matters more from this release on. Once created_at is a timestamptz, DATE(created_at)
		// resolves in the session zone, and so does any comparison against a Go-supplied time. A
		// streak counting 10:00 UTC as the previous day because the host is at +05:30 is not a
		// cosmetic difference: it moves work to the wrong square of the heatmap and can break a
		// streak that was real.
		//
		// The queries that depend on this also say AT TIME ZONE 'UTC' explicitly, and the duplication
		// is intentional rather than redundant. Here is the floor, so that a future query written
		// without the clause is still correct; there is the statement of intent, so a reader of the
		// query does not have to know the connection is configured at all. A test asserts the
		// behavior survives even if this hook is removed.
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			if _, err := conn.Exec(ctx, `SET TIME ZONE 'UTC'`); err != nil {
				return fmt.Errorf("pin the session time zone: %w", err)
			}
			return nil
		}

		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			// Ping to verify connectivity
			if perr := pool.Ping(ctx); perr == nil {
				cancel()
				Pool = pool
				return nil
			} else {
				pool.Close()
				lastErr = perr
			}
		} else {
			lastErr = err
		}

		cancel()

		// If not last attempt, wait a bit then retry
		if i < attempts {
			time.Sleep(delay)
		}
	}

	return fmt.Errorf("failed to connect to database after %d attempts: %w", attempts, lastErr)
}

// RequireSchema verifies that the schema the application needs already exists.
//
// The application used to create its own schema on boot, with the DDL hardcoded here. That coupled
// a read-mostly web service to DDL privileges for the whole of its life: the credential it held
// could drop tables, not just read and write poems. It also meant the schema existed in two places,
// this file and the migrations directory, with nothing keeping them equal.
//
// Schema is now created only by the migration runner, and the application holds no DDL rights. The
// cost of that separation is that a deploy can arrive before its migrations have run, so this check
// exists to turn that into a clear message rather than a missing-relation error on the first query.
//
// The check is deliberately limited to the one table the application cannot start without. It does
// not consult schema_migrations: how the schema is versioned is the runner's business, and reading
// its bookkeeping table would reintroduce exactly the coupling this change removes. A schema that
// exists but is only partly migrated is therefore not detected here; the runner is responsible for
// applying migrations completely or failing.
func RequireSchema(ctx context.Context) error {
	if err := Require(); err != nil {
		return err
	}

	var table *string
	if err := Pool.QueryRow(ctx, `SELECT to_regclass('poems')::text`).Scan(&table); err != nil {
		return fmt.Errorf("could not check for the poems table: %w", err)
	}

	if table == nil {
		return errors.New(
			"the poems table does not exist, so the database has not been migrated; " +
				"run the migrations before starting the service: go run ./cmd/migrate")
	}

	return nil
}

// Ping reports whether the database is currently reachable.
//
// This backs the platform health probe, and it exists because a probe that cannot fail is not a
// probe. A handler that answers 200 without consulting the database reports a healthy instance while
// every page behind it is returning errors, so the platform neither restarts nor alerts, and the log
// shows an unbroken stream of 200s for the whole outage.
//
// A nil pool is reported as unreachable rather than dereferenced. /health is registered before the
// pool is connected during boot, and a probe that panics takes the process down instead of reporting
// that it is not ready.
func Ping(ctx context.Context) error {
	if err := Require(); err != nil {
		return err
	}
	return Pool.Ping(ctx)
}
