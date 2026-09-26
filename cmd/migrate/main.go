package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/divijg19/Verse/internal/database"
)

func main() {
	if err := database.Connect(); err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer func() {
		if database.Pool != nil {
			database.Pool.Close()
		}
	}()

	if err := runMigrations(context.Background(), "migrations"); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
}

func runMigrations(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)

	for _, name := range files {
		// #nosec G304 -- the path is not attacker-controlled. `dir` is the migrations directory
		// passed by this command, and `name` came from os.ReadDir of that same directory, so the
		// value can only ever be a file that already exists inside it. gosec cannot see that
		// provenance, only that the argument is a variable.
		path := filepath.Join(dir, name)
		sqlBytes, err := os.ReadFile(path) // #nosec G304
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		sql := strings.TrimSpace(string(sqlBytes))
		if sql == "" {
			continue
		}

		if _, err := database.Pool.Exec(ctx, sql); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		log.Printf("applied %s", name)
	}

	return nil
}
