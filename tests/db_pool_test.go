package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/migrate"
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
