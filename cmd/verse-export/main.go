// Command verse-export writes a complete copy of the work outside the database.
//
// It exists because the writing had no way out of the application. Reading it in a browser, editing
// it, and hand-written SQL were the only options, so a provider incident or a mistaken migration was
// total loss. Run it on a schedule and the database stops being the only copy.
//
//	go run ./cmd/verse-export                      > verse-2026-09-27.json
//	go run ./cmd/verse-export -format md          > verse-2026-09-27.md
//	go run ./cmd/verse-export -include-deleted     > verse-full.json
//	go run ./cmd/verse-export -out verse.json
//
// JSON is the archival format and is lossless. Markdown is for reading and deliberately is not; it
// cannot distinguish a deleted work from a live one, nor carry the retained revisions, without
// ceasing to be prose.
//
// Reads DATABASE_URL, like every other command here. The export is a bulk read of everything, so
// point it at a replica or a copy if you have one -- this is the one command where reading from a
// different database than the service is routine rather than a mistake.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/export"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("verse: export failed: %v", err)
	}
}

func run() error {
	format := flag.String("format", "json", "output format: json (lossless) or md (for reading)")
	out := flag.String("out", "", "write to this file instead of stdout")
	includeDeleted := flag.Bool("include-deleted", false,
		"include soft-deleted works; needed for a complete archival copy")
	flag.Parse()

	writer, ok := export.ByName(*format)
	if !ok {
		return fmt.Errorf("unknown format %q; want json or md", *format)
	}

	if err := database.Connect(); err != nil {
		return err
	}
	defer func() {
		if database.Pool != nil {
			database.Pool.Close()
		}
	}()

	doc, err := export.Build(context.Background(), *includeDeleted)
	if err != nil {
		return err
	}

	body, err := writer.Write(doc)
	if err != nil {
		return err
	}

	if *out == "" {
		if _, err := os.Stdout.Write(body); err != nil {
			return err
		}
		if _, err := os.Stdout.Write([]byte("\n")); err != nil {
			return err
		}
	} else if err := os.WriteFile(*out, body, 0o600); err != nil {
		return err
	}

	// Reported on stderr so that piping stdout to a file does not capture the summary into the
	// archive. Poem counts, not content: this is a log line.
	revisions := 0
	for _, p := range doc.Poems {
		revisions += len(p.Versions)
	}
	log.Printf("exported %d work(s) and %d retained revision(s) at %s",
		len(doc.Poems), revisions, time.Now().UTC().Format(time.RFC3339))

	return nil
}
