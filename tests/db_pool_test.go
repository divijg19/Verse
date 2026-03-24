package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/database"
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
	if err := database.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("database ensure schema failed: %v", err)
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

func TestEnsureSchemaCreatesActivePoemTimelineIndex(t *testing.T) {
	dsn := requireTestDSN(t)

	t.Setenv("DATABASE_URL", dsn)

	if database.Pool != nil {
		database.Pool.Close()
		database.Pool = nil
	}

	if err := database.Connect(); err != nil {
		t.Fatalf("database connect failed: %v", err)
	}
	if err := database.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("database ensure schema failed: %v", err)
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
		WHERE schemaname = 'public'
		AND tablename = 'poems'
		AND indexname = 'idx_poems_active_created_at'`).Scan(&indexDef)
	if err != nil {
		t.Fatalf("lookup index definition failed: %v", err)
	}

	if !strings.Contains(indexDef, "created_at DESC") || !strings.Contains(indexDef, "deleted_at IS NULL") {
		t.Fatalf("unexpected index definition: %q", indexDef)
	}
}
