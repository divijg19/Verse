// Command publish builds the public static site from the published works.
//
// It is a build-time command, not a server. It reads the published set through the same service the
// list uses, closes the database pool, renders every page in memory, validates the result, and
// only then replaces the output directory. Readers never receive a database credential, and the
// deployed artifact does not depend on Render or Neon being reachable.
//
//	go run ./cmd/publish -list
//	go run ./cmd/publish -out dist
//	go run ./cmd/publish -out dist -base-url https://example.com
//	go run ./cmd/publish -out dist -links "Studio=https://studio.example"
//
// The -links flag fills the brand pill's menu with the author's other portfolio sites. The reader's
// own navigation is not in that menu; it is on the screen, beside the content.
//
// The -tagline flag is the front screen's line: what the site is, rather than a summary of what it
// holds. -description is the supporting sentence underneath it.
//
// Reads DATABASE_URL, like every other command here. Use a read-only serving credential for a real
// build; the command only issues the publisher's two SELECTs.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/publish"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("verse publish: %v", err)
	}
}

func run() error {
	out := flag.String("out", "dist", "directory to write the static site into")
	list := flag.Bool("list", false, "list the published works and write nothing")
	baseURL := flag.String("base-url", envOr("PUBLIC_BASE_URL", publish.DefaultBaseURL),
		"absolute public origin used by sitemap.xml and feed.xml")
	title := flag.String("title", publish.DefaultTitle, "public site title")
	description := flag.String("description", publish.DefaultDescription, "public site description")
	tagline := flag.String("tagline", envOr("PUBLIC_TAGLINE", ""), "the front screen's line")
	databaseURL := flag.String("database-url", "", "database URL; defaults to DATABASE_URL")
	from := flag.String("from", "",
		"build from this export file instead of the database; the file is written by `verse-export --public`")
	links := flag.String("links", envOr("PUBLIC_LINKS", ""),
		"portfolio sites for the brand pill's menu, as Label=URL pairs separated by commas")

	flag.Parse()

	if *databaseURL != "" {
		if err := os.Setenv("DATABASE_URL", *databaseURL); err != nil {
			return fmt.Errorf("set DATABASE_URL from -database-url: %w", err)
		}
	}

	// A full library read is bounded, but it is still a network operation. The timeout turns a
	// database that has stopped answering into a failed build rather than a hung CI job.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// A build from a file needs no database at all, and does not open one. That is the whole point
	// of the file path: the build environment can hold no database credential, because it never
	// speaks to a database.
	if *from == "" {
		if err := database.Connect(); err != nil {
			return err
		}
		defer database.ClosePool()
	}

	if *list {
		return reportList(ctx)
	}

	if *baseURL == publish.DefaultBaseURL {
		fmt.Fprintf(os.Stderr,
			"warning: using the placeholder public origin %s; set -base-url or PUBLIC_BASE_URL before deploying\n",
			publish.DefaultBaseURL)
	}

	portfolio, err := publish.ParseLinks(*links)
	if err != nil {
		return err
	}

	// The options are the same either way. Only where the published set comes from differs, so
	// the difference is expressed once, here, rather than by duplicating the whole build.
	opts := publish.Options{
		Title:       *title,
		Description: *description,
		Tagline:     *tagline,
		BaseURL:     *baseURL,
		Links:       portfolio,
	}

	var report publish.Report
	if *from != "" {
		report, err = publish.BuildFile(ctx, *from, *out, opts)
	} else {
		report, err = publish.Build(ctx, *out, opts)
	}
	if err != nil {
		return err
	}

	fmt.Printf("built %d works into %s: %d files, %d bytes\n",
		report.Works, report.Out, report.Files, report.Bytes)
	return nil
}

// reportList prints the published works.
//
// It was the dry run once, and the name has moved with the question it answers. The dry run was
// "what would publishing expose", asked before anything wrote to published_at. Publication is an
// explicit act now, so this reports what is out there -- which is what an operator needs when the
// site and the library disagree.
func reportList(ctx context.Context) error {
	result, err := publish.List(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("published works: %d (counted %d)\n", result.Count, result.Counted)
	for _, entry := range result.Entries {
		fmt.Printf("%s  %8d  %s  %s\n",
			entry.PublishedAt.UTC().Format("2006-01-02"), entry.SizeBytes, entry.ID, entry.Title)
	}

	if !result.Complete() {
		return fmt.Errorf("the published set is incomplete: %d listed, %d counted", result.Count, result.Counted)
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
