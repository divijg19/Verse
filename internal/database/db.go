package database

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var Pool *pgxpool.Pool

func Connect() error {

	databaseURL := os.Getenv("DATABASE_URL")

	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return err
	}

	config.MaxConns = 10
	config.MinConns = 1
	config.MaxConnLifetime = time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return err
	}

	// test connection
	if err := pool.Ping(ctx); err != nil {
		return err
	}

	Pool = pool

	fmt.Println("Database connected")

	return nil
}
