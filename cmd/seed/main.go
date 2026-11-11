// Command seed fills a local database with plausible dummy work.
//
// Local development needs content: an empty dashboard has no heatmap and a streak of one, an empty
// library cannot be searched, and a public site with no published work has no index, no archive and
// no work page to look at. None of those screens can be judged in that state.
//
//	go run ./cmd/seed -reset
//
// It refuses to run against a database that is not on this machine. Seeding a hosted database would
// replace the author's work with generated text, and no version of that is what somebody meant.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/seed"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("verse: seed failed: %v", err)
	}
}

func run() error {
	reset := flag.Bool("reset", false, "empty the library before writing")
	count := flag.Int("count", 0, "how many live works to write; 0 takes the default, and a negative number writes none")
	published := flag.Int("published", 0, "how many to publish; 0 derives it from the count, and a negative number publishes none")
	deleted := flag.Int("deleted", 0, "how many to soft-delete; 0 derives it from the count, and a negative number deletes none")
	random := flag.Uint64("seed", 1, "the seed the library is generated from")
	days := flag.Int("days", 0, "how many days back the library reaches; 0 takes the default")
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set")
	}
	if err := seed.RequireLocalDSN(dsn); err != nil {
		return err
	}
	if os.Getenv(seed.ForceRemoteEnv) == "1" {
		log.Printf("%s is set, so the database is not checked for locality", seed.ForceRemoteEnv)
	}

	if err := database.Connect(); err != nil {
		return err
	}
	defer database.ClosePool()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	report, err := seed.Run(ctx, database.Pool, seed.Options{
		Count:     *count,
		Published: *published,
		Deleted:   *deleted,
		Seed:      *random,
		Days:      *days,
		Reset:     *reset,
	})
	if err != nil {
		return err
	}

	log.Printf("wrote %d works: %d published, %d deleted, %d revisions",
		report.Live, report.Published, report.Deleted, report.Versions)
	return nil
}
