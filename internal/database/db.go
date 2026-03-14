package database

import (
	"context"
	"fmt"
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
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.MaxConns = int32(n)
			}
		} else {
			cfg.MaxConns = 5
		}
		if v := os.Getenv("DB_MIN_CONNS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				cfg.MinConns = int32(n)
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
				if serr := ensurePoemsSchema(ctx, pool); serr != nil {
					pool.Close()
					cancel()
					return fmt.Errorf("failed to ensure poems schema: %w", serr)
				}

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

func ensurePoemsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS poems (
			id UUID PRIMARY KEY,
			content TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT now()
		)`); err != nil {
		return err
	}

	if _, err := pool.Exec(ctx, `ALTER TABLE poems ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP NULL`); err != nil {
		return err
	}

	return nil
}
