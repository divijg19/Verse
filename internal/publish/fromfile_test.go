package publish_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/export"
	"github.com/divijg19/Verse/internal/publish"
)

// The two paths are the property the whole push topology rests on.
//
// The public site can be built from a database or from an export file, and the two have to produce
// the same bytes. If they did not, then a deploy built from the file would be a different site
// from one built from the database, and the file path would be a second, quieter publisher with its
// own idea of what is public.
//
// The comparison is byte for byte across every output, not across a page: the feed, the sitemap,
// the search index and the stylesheet all have to agree too.

// filePathOptions is the configuration both paths are rendered with, so that the comparison is
// about the source of the works and nothing else.
func filePathOptions() publish.Options {
	return publish.Options{
		Title:       "Verse",
		Description: "A selection of published work.",
		Tagline:     "Poems, and the literary devices they are built from.",
		BaseURL:     "https://verse.test",
	}
}

// writeExport writes a document to a temporary file and returns its path.
func writeExport(t *testing.T, doc export.Document) string {
	t.Helper()

	doc.Digest = export.PublishedDigest(doc.Poems)

	body, err := export.JSON{}.Write(doc)
	if err != nil {
		t.Fatalf("serialize the export: %v", err)
	}

	path := filepath.Join(t.TempDir(), "verse-export.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write the export file: %v", err)
	}
	return path
}

// seedAndExport fills a scratch schema with a library and writes it out as an export file.
//
// The library holds a published work, a second published work, a draft and a work that was published
// and then withdrawn -- the four states the publisher's filter has to tell apart.
func seedAndExport(t *testing.T, ctx context.Context, scratch *pgxpool.Pool) string {
	t.Helper()

	seed(t, ctx, scratch,
		seedPoem{id: "bbbbbbbb-0000-0000-0000-000000000021", content: "A Published Work\n\npublic body", published: true},
		seedPoem{id: "bbbbbbbb-0000-0000-0000-000000000022", content: "Another Published Work\n\nmore body", published: true},
		seedPoem{id: "bbbbbbbb-0000-0000-0000-000000000023", content: "A Draft Work\n\ndraft body"},
		seedPoem{id: "bbbbbbbb-0000-0000-0000-000000000024", content: "A Withdrawn Work\n\nwithdrawn body", published: true, deleted: true},
	)

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	doc, err := export.Build(ctx, export.Options{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("build the export: %v", err)
	}
	return writeExport(t, doc)
}

// TestTheFilePathPublishesExactlyWhatTheDatabasePathDoes is the boundary itself.
//
// The file holds a draft and a withdrawn work, and neither may appear. The publisher's filter is the
// security boundary of the whole author/viewer split, and a file path that skipped it would be a
// way to publish the library.
func TestTheFilePathPublishesExactlyWhatTheDatabasePathDoes(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "fromfile_set")
	path := seedAndExport(t, ctx, scratch)

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	fromFile, _, err := publish.LoadFile(path)
	if err != nil {
		t.Fatalf("load the export file: %v", err)
	}

	if len(fromFile) != 2 {
		t.Fatalf("the file path published %d works, want 2", len(fromFile))
	}

	for _, work := range fromFile {
		switch work.Title {
		case "A Published Work", "Another Published Work":
		default:
			t.Errorf("the file path published %q, which is not one of the two published works", work.Title)
		}
	}
}

// TestTheTwoPathsRenderIdenticalBytes is the assertion the deploy step depends on.
//
// The same published works, rendered from the database and from an export file, have to produce the
// same bytes in every output. This is what makes the file a usable artifact: the file path is not a
// second publisher, it is the same publisher reading from somewhere else.
func TestTheTwoPathsRenderIdenticalBytes(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "fromfile_bytes")
	path := seedAndExport(t, ctx, scratch)

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	fromDatabase, _, err := publish.Load(ctx)
	if err != nil {
		t.Fatalf("load from the database: %v", err)
	}
	fromFile, _, err := publish.LoadFile(path)
	if err != nil {
		t.Fatalf("load the export file: %v", err)
	}

	if len(fromDatabase) != len(fromFile) {
		t.Fatalf("the database path published %d works and the file path %d", len(fromDatabase), len(fromFile))
	}

	opts := filePathOptions()

	databaseFiles, err := publish.Render(fromDatabase, opts)
	if err != nil {
		t.Fatalf("render from the database: %v", err)
	}
	fileFiles, err := publish.Render(fromFile, opts)
	if err != nil {
		t.Fatalf("render from the file: %v", err)
	}

	if len(databaseFiles) != len(fileFiles) {
		t.Errorf("the database path wrote %d files and the file path %d", len(databaseFiles), len(fileFiles))
	}

	for name, body := range databaseFiles {
		other, ok := fileFiles[name]
		if !ok {
			t.Errorf("the file path did not write %q", name)
			continue
		}
		if !bytes.Equal(body, other) {
			t.Errorf("%q differs between the two paths:\n  database: %q\n  file:     %q",
				name, string(body), string(other))
		}
	}
}

// TestTheFilePathRefusesWhatItCannotRead covers the refusals.
//
// The artifact crosses a trust boundary: one component writes it and another reads it. A file that
// is not an export, is an export of a version this build does not read, or has been changed since
// it was written, has to be refused rather than read as something it is not.
func TestTheFilePathRefusesWhatItCannotRead(t *testing.T) {
	t.Run("not an export", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nope.json")
		if err := os.WriteFile(path, []byte(`{"format":"something-else","version":2}`), 0o600); err != nil {
			t.Fatalf("write the file: %v", err)
		}
		if _, _, err := publish.LoadFile(path); err == nil {
			t.Fatal("the file path accepted a file that is not an export")
		}
	})

	t.Run("a version this build does not read", func(t *testing.T) {
		doc := export.Document{
			Format:  export.Format,
			Version: 1,
			Poems:   []export.Poem{},
		}
		path := writeExport(t, doc)
		if _, _, err := publish.LoadFile(path); err == nil {
			t.Fatal("the file path accepted a version 1 export, which cannot say what is published")
		}
	})

	t.Run("a file changed after it was written", func(t *testing.T) {
		published := time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)
		doc := export.Document{
			Format:  export.Format,
			Version: export.FormatVersion,
			Poems: []export.Poem{{
				ID:          "cccccccc-0000-0000-0000-000000000031",
				Content:     "A Published Work\n\nA body.",
				CreatedAt:   published,
				PublishedAt: &published,
				Versions:    []export.Version{},
			}},
		}
		path := writeExport(t, doc)

		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read the file: %v", err)
		}
		if err := os.WriteFile(path, bytes.Replace(body, []byte("A body"), []byte("Another body"), 1), 0o600); err != nil {
			t.Fatalf("write the file: %v", err)
		}

		if _, _, err := publish.LoadFile(path); err == nil {
			t.Fatal("the file path accepted an export whose contents no longer match its digest")
		}
	})
}

// TestTheDigestCoversThePublishedSetOnly checks what the digest is for.
//
// Two exports of the same library whose drafts differ have the same digest, because a draft is not
// visible. Two exports whose published works differ do not.
func TestTheDigestCoversThePublishedSetOnly(t *testing.T) {
	published := time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)
	poems := []export.Poem{
		{
			ID:          "dddddddd-0000-0000-0000-000000000041",
			Content:     "A Published Work\n\nA body.",
			CreatedAt:   published,
			PublishedAt: &published,
		},
		{
			ID:        "dddddddd-0000-0000-0000-000000000042",
			Content:   "A Draft Work\n\ndraft body.",
			CreatedAt: published,
		},
	}
	first := export.PublishedDigest(poems)

	// A change to a draft.
	changed := append([]export.Poem(nil), poems...)
	changed[1].Content = "A Different Draft\n\nstill a draft."
	if second := export.PublishedDigest(changed); second != first {
		t.Error("the digest changed when a draft changed; a draft is not visible")
	}

	// A change to a published work.
	changed = append([]export.Poem(nil), poems...)
	changed[0].Content = "A Published Work\n\nA different body."
	if second := export.PublishedDigest(changed); second == first {
		t.Error("the digest did not change when a published work changed")
	}
}

// TestTheExcerptIsDrawnFromTheBody checks the excerpt's derivation.
//
// The title is already the heading on the page and the title of the document, so an excerpt that
// began with it repeated it. Every work's title is its first line, so the old excerpt was the title
// followed by the whole poem run together.
func TestTheExcerptIsDrawnFromTheBody(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "fromfile_excerpt")
	seed(t, ctx, scratch,
		seedPoem{id: "eeeeeeee-0000-0000-0000-000000000051", content: "A Published Work\n\nA body that belongs to it.", published: true},
	)

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	works, _, err := publish.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(works) != 1 {
		t.Fatalf("loaded %d works, want 1", len(works))
	}

	excerpt := works[0].Excerpt
	if excerpt == "" {
		t.Fatal("the excerpt is empty")
	}
	if bytes.Contains([]byte(excerpt), []byte("A Published Work")) {
		t.Errorf("the excerpt repeats the title: %q", excerpt)
	}
	if !bytes.Contains([]byte(excerpt), []byte("A body that belongs to it.")) {
		t.Errorf("the excerpt is not the body: %q", excerpt)
	}
}
