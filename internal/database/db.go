package database

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is the global database connection pool.
var Pool *pgxpool.Pool

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
	if Pool == nil {
		return errors.New("database not initialized")
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
