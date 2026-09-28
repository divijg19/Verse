package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/migrate"
	"github.com/divijg19/Verse/internal/testsupport"
)

func TestDatabasePoolDefaults(t *testing.T) {
	dsn := requireTestDSN(t)

	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("DB_MAX_CONNS", "")
	t.Setenv("DB_MIN_CONNS", "")
	t.Setenv("DB_MAX_CONN_IDLE", "")

	if database.Pool != nil {
		database.Pool.Close()
		database.Pool = nil
	}

	if err := database.Connect(); err != nil {
		t.Fatalf("database connect failed: %v", err)
	}
	if _, err := migrate.Run(context.Background(), database.Pool); err != nil {
		t.Fatalf("database migrate: %v", err)
	}
	t.Cleanup(func() {
		if database.Pool != nil {
			database.Pool.Close()
			database.Pool = nil
		}
	})

	cfg := database.Pool.Config()
	if cfg.MaxConns != 5 {
		t.Fatalf("MaxConns = %d, want 5", cfg.MaxConns)
	}
	if cfg.MinConns != 1 {
		t.Fatalf("MinConns = %d, want 1", cfg.MinConns)
	}
	if cfg.MaxConnIdleTime != 5*time.Minute {
		t.Fatalf("MaxConnIdleTime = %s, want 5m", cfg.MaxConnIdleTime)
	}
}

// TestMigrationsCreateActivePoemTimelineIndex asserts the index the application relies on exists
// after migrations.
//
// This previously tested EnsureSchema, the DDL the server used to run on boot. It now tests the
// migration runner, because that is the only thing that creates schema. The assertions are
// unchanged: the index and its definition are what matter, not which code path made them.
func TestMigrationsCreateActivePoemTimelineIndex(t *testing.T) {
	dsn := requireTestDSN(t)

	t.Setenv("DATABASE_URL", dsn)

	if database.Pool != nil {
		database.Pool.Close()
		database.Pool = nil
	}

	if err := database.Connect(); err != nil {
		t.Fatalf("database connect failed: %v", err)
	}
	if _, err := migrate.Run(context.Background(), database.Pool); err != nil {
		t.Fatalf("database migrate: %v", err)
	}
	t.Cleanup(func() {
		if database.Pool != nil {
			database.Pool.Close()
			database.Pool = nil
		}
	})

	var indexDef string
	err := database.Pool.QueryRow(context.Background(), `
		SELECT indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
		AND tablename = 'poems'
		AND indexname = 'idx_poems_active_created_at'`).Scan(&indexDef)
	if err != nil {
		t.Fatalf("lookup index definition failed: %v", err)
	}

	if !strings.Contains(indexDef, "created_at DESC") || !strings.Contains(indexDef, "deleted_at IS NULL") {
		t.Fatalf("unexpected index definition: %q", indexDef)
	}
}

// TestPooledSessionsArePinnedToUTC asserts the AfterConnect hook in internal/database, and arranges
// for the assertion to mean something on any host.
//
// The pin exists because pgx reads the Postgres-side PGTZ and not the Go-side TZ, so a session's zone
// is whatever the database server was configured with. Two hosts holding the same data could then
// read the same poem onto different calendar days, which is not a display difference: it moves work
// between heatmap squares and can break a streak that was real.
//
// A test that simply asserted the zone is UTC would pass on a UTC server whether or not the hook
// existed, which is exactly where CI runs. So the server default is moved away from UTC first, and
// the assertion becomes one the hook has to earn. The role's setting is restored afterwards, because
// these tests share a database.
func TestPooledSessionsArePinnedToUTC(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()

	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("DB_MAX_CONNS", "")
	t.Setenv("DB_MIN_CONNS", "")
	t.Setenv("DB_MAX_CONN_IDLE", "")

	// A connection of our own, to move the role's default and to read the role's name back. A
	// parameter cannot be used for an identifier, hence the quoting helper.
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	var role string
	if err := admin.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil {
		admin.Close()
		t.Fatalf("read the current role: %v", err)
	}
	ident := testsupport.QuoteIdent(role)
	if _, err := admin.Exec(ctx, `ALTER ROLE `+ident+` SET TimeZone TO 'Asia/Kolkata'`); err != nil {
		admin.Close()
		t.Fatalf("move the role's default zone: %v", err)
	}
	t.Cleanup(func() {
		// Best effort on a connection that is about to close: a failure here means the role keeps a
		// non-UTC default, which is worth surfacing but not worth failing an unrelated test over.
		if _, err := admin.Exec(ctx, `ALTER ROLE `+ident+` RESET TimeZone`); err != nil {
			t.Errorf("restore the role's default time zone: %v", err)
		}
		admin.Close()
	})

	if database.Pool != nil {
		database.Pool.Close()
		database.Pool = nil
	}
	if err := database.Connect(); err != nil {
		t.Fatalf("database connect failed: %v", err)
	}
	t.Cleanup(func() {
		if database.Pool != nil {
			database.Pool.Close()
			database.Pool = nil
		}
	})

	// Every connection, not one. A pool hands out idle connections first, so a single sample could be
	// a connection that happened to be opened when the zone was already UTC for another reason.
	conns := database.Pool.AcquireAllIdle(ctx)
	if len(conns) == 0 {
		t.Fatal("the pool has no idle connection to check")
	}
	defer func() {
		for _, c := range conns {
			c.Release()
		}
	}()

	for i, c := range conns {
		var got string
		if err := c.QueryRow(ctx, `SHOW TimeZone`).Scan(&got); err != nil {
			t.Fatalf("read the zone on connection %d: %v", i, err)
		}
		// Postgres accepts several spellings of UTC, so the comparison is on the offset rather than
		// the name: a session at +00:00 is UTC for every purpose this release depends on.
		var offsetSeconds int
		if err := c.QueryRow(ctx,
			`SELECT EXTRACT(TIMEZONE FROM now())::int`).Scan(&offsetSeconds); err != nil {
			t.Fatalf("read the zone offset on connection %d: %v", i, err)
		}
		if offsetSeconds != 0 {
			// The offset is reported in seconds rather than as hours. Integer division of a negative
			// offset truncates towards zero, so -18000/3600 is 0, and an hours-and-minutes rendering
			// would report a zone five hours *behind* UTC as "UTC+0000" -- which is the one message
			// here most likely to mislead whoever is reading it.
			t.Fatalf("connection %d is %s, which is %+d seconds from UTC (%d:%02d), although the "+
				"role's default was moved to Asia/Kolkata; the pool is not pinning sessions to UTC",
				i, got, offsetSeconds, offsetSeconds/3600, abs(offsetSeconds%3600)/60)
		}
	}
}

// abs returns the magnitude of n, so a failure message can render a negative offset's remainder
// without a second helper and an import.
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
