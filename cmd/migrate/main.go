// Command migrate applies the embedded SQL migrations to the database named by DATABASE_URL.
//
// This is the only thing that creates schema. The application itself no longer holds DDL rights, so
// running this is a deploy step rather than a side effect of starting the service.
//
// It is safe to run repeatedly: migrations already applied are verified and skipped.
package main

import (
	"context"
	"log"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/migrate"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("verse: migration failed: %v", err)
	}
}

func run() error {
	if err := database.Connect(); err != nil {
		return err
	}
	defer func() {
		if database.Pool != nil {
			database.Pool.Close()
		}
	}()

	result, err := migrate.Run(context.Background(), database.Pool)
	if err != nil {
		return err
	}

	for _, name := range result.Applied {
		log.Printf("applied %s", name)
	}
	for _, name := range result.Skipped {
		log.Printf("already applied %s", name)
	}

	// A migration run that applied nothing is not obviously different from one that was never
	// attempted, so it is stated explicitly.
	if len(result.Applied) == 0 {
		log.Printf("schema is up to date; %d migration(s) already applied", len(result.Skipped))
	}

	return nil
}
