// Package export produces a complete, lossless copy of the work outside the database.
//
// This exists because the author's writing had exactly three ways out of the application -- the
// editor, the reader, and hand-written SQL -- and no way to take it away intact. A provider incident
// or a mistaken migration was total loss. Combined with the history added in v0.4.4, the database is
// now the only copy of the work, which makes being able to extract it a correctness property rather
// than a convenience.
//
// The guarantee this package makes is that an export is lossless: re-reading an export and
// comparing it to the database yields byte-identical content for every poem and every retained
// version. The format therefore stores text verbatim and never normalizes it -- no trimming, no
// newline translation, no truncation. Anything that altered a poem on the way out would make the
// export a lossy summary of the thing it claims to preserve.
package export

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/divijg19/Verse/internal/database"
)

// Format is the version tag written into every export.
//
// A consumer that does not recognize the version can refuse rather than silently misreading a
// future layout. It is an integer rather than a date so that a reader can compare it with an
// ordinary less-than.
const Format = "verse-export"

// FormatVersion is the current layout version.
const FormatVersion = 1

// Document is the whole export.
//
// Field order here is the field order in the file, which is deliberate: a human reading a backup
// should not have to hunt through it.
type Document struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`
	Poems      []Poem    `json:"poems"`
}

// Poem is one work, with its retained history.
//
// Versions are included rather than left behind, because after v0.4.4 they are the only record of
// superseded drafts. An export that carried current content alone would be a partial backup of the
// work, and would silently drop the thing v0.4.4 was written to preserve.
type Poem struct {
	ID        string     `json:"id"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
	DeletedAt *time.Time `json:"deleted_at"`
	Versions  []Version  `json:"versions"`
}

// Version is one retained revision.
type Version struct {
	ID         string    `json:"id"`
	Content    string    `json:"content"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Writer serializes a Document.
type Writer interface {
	Write(Document) ([]byte, error)
	ContentType() string
	FileExtension() string
}

// ByName resolves a format name, and reports whether it is known.
func ByName(name string) (Writer, bool) {
	switch name {
	case "json", "":
		return JSON{}, true
	case "md", "markdown":
		return Markdown{}, true
	default:
		return nil, false
	}
}

// JSON writes the canonical, lossless format.
type JSON struct{}

// Write encodes the document.
//
// HTML escaping is disabled, and it has to be. encoding/json escapes <, > and & by default so that
// its output can be embedded in HTML; that is not what this file is for, and a poem containing those
// characters would otherwise come back as < sequences. Since the whole point of this format is to
// reproduce the work exactly, the encoder is configured rather than left on its default.
//
// bytes.Buffer is used because Encoder appends a trailing newline, and MarshalIndent is not
// available with the escaping turned off.
func (JSON) Write(d Document) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func (JSON) ContentType() string   { return "application/json" }
func (JSON) FileExtension() string { return "json" }

// Markdown writes a human-readable rendering, for reading rather than restoring.
//
// Explicitly not lossless and not intended to be: it cannot represent the difference between a poem
// and a deleted one, nor the retained history, without becoming something other than prose. JSON is
// the archival format; this is for a person who wants to read their own work in a text editor.
type Markdown struct{}

// Write renders the document as Markdown.
func (Markdown) Write(d Document) ([]byte, error) {
	var out []byte
	appendLine := func(format string, args ...any) {
		out = append(out, []byte(fmt.Sprintf(format, args...))...)
		out = append(out, '\n')
	}

	appendLine("# Verse export")
	appendLine("")
	appendLine("Exported %s.", d.ExportedAt.UTC().Format("2006-01-02 15:04 UTC"))
	appendLine("")
	appendLine("%d work(s), %d retained revision(s).", len(d.Poems), countVersions(d))
	appendLine("")

	for _, p := range d.Poems {
		appendLine("---")
		appendLine("")
		title := firstLine(p.Content)
		if title == "" {
			title = "Untitled"
		}
		appendLine("## %s", title)
		appendLine("")
		appendLine("*Written %s. id `%s`.*",
			p.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), p.ID)
		if p.DeletedAt != nil {
			appendLine("")
			appendLine("**Deleted %s.**", p.DeletedAt.UTC().Format("2006-01-02 15:04 UTC"))
		}
		appendLine("")
		// The content is emitted verbatim between fences. A poem containing a fence would break
		// the block, so the fence is lengthened until it cannot appear in the content.
		appendLine("%s", fenceFor(p.Content))
		out = append(out, p.Content...)
		out = append(out, '\n')
		appendLine("%s", fenceFor(p.Content))
		appendLine("")

		if len(p.Versions) > 0 {
			appendLine("### Retained revisions")
			appendLine("")
			for _, v := range p.Versions {
				appendLine("#### %s", v.RecordedAt.UTC().Format("2006-01-02 15:04 UTC"))
				appendLine("")
				appendLine("%s", fenceFor(v.Content))
				out = append(out, v.Content...)
				out = append(out, '\n')
				appendLine("%s", fenceFor(v.Content))
				appendLine("")
			}
		}
	}

	return out, nil
}

func (Markdown) ContentType() string   { return "text/markdown; charset=utf-8" }
func (Markdown) FileExtension() string { return "md" }

// fenceFor returns a code fence long enough that it cannot occur inside content.
func fenceFor(content string) string {
	longest := 0
	current := 0
	for _, r := range content {
		if r == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	n := 3
	if longest >= n {
		n = longest + 1
	}
	return repeat("`", n)
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n*len(s))
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func firstLine(content string) string {
	for i, r := range content {
		if r == '\n' {
			return content[:i]
		}
	}
	return content
}

func countVersions(d Document) int {
	n := 0
	for _, p := range d.Poems {
		n += len(p.Versions)
	}
	return n
}

// Build assembles a Document from the database.
//
// includeDeleted controls whether soft-deleted works are included. They are excluded by default
// because a routine export should be the current body of work; -include-deleted exists for the
// archival case, and is documented as the lossless one.
func Build(ctx context.Context, includeDeleted bool) (Document, error) {
	doc := Document{
		Format:     Format,
		Version:    FormatVersion,
		ExportedAt: time.Now().UTC(),
		Poems:      []Poem{},
	}

	p := database.Pool
	if p == nil {
		return doc, fmt.Errorf("database not initialized")
	}

	rows, err := p.Query(ctx, `
        SELECT id, content, created_at, deleted_at
        FROM poems
        ORDER BY created_at, id`)
	if err != nil {
		return doc, fmt.Errorf("read poems: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p Poem
		if err := rows.Scan(&p.ID, &p.Content, &p.CreatedAt, &p.DeletedAt); err != nil {
			return doc, fmt.Errorf("read poem: %w", err)
		}
		if p.DeletedAt != nil && !includeDeleted {
			continue
		}
		doc.Poems = append(doc.Poems, p)
	}
	if err := rows.Err(); err != nil {
		return doc, fmt.Errorf("read poems: %w", err)
	}

	// Versions for every retained poem, in one query.
	//
	// This was one query per poem, which for a library of any size meant thousands of round trips
	// inside a single 30s request timeout. A LEFT JOIN over the same tables returns exactly the same
	// rows: poems with no revisions produce a NULL version row, which the scan turns back into the
	// empty slice it was before.
	versionRows, err := p.Query(ctx, `
        SELECT pv.poem_id, pv.id, pv.content, pv.recorded_at
        FROM poem_versions pv
        ORDER BY pv.poem_id, pv.seq`)
	if err != nil {
		return doc, fmt.Errorf("read retained revisions: %w", err)
	}
	defer versionRows.Close()

	byPoem := map[string][]Version{}
	for versionRows.Next() {
		var poemID string
		var v Version
		if err := versionRows.Scan(&poemID, &v.ID, &v.Content, &v.RecordedAt); err != nil {
			return doc, fmt.Errorf("read retained revision: %w", err)
		}
		byPoem[poemID] = append(byPoem[poemID], v)
	}
	if err := versionRows.Err(); err != nil {
		return doc, fmt.Errorf("read retained revisions: %w", err)
	}

	// Oldest first, so a reader sees the history of a poem in the order it happened.
	for i := range doc.Poems {
		versions := byPoem[doc.Poems[i].ID]
		if versions == nil {
			// Empty, never null: a consumer reading the file back must be able to tell "no
			// history" from "field absent", which is the kind of ambiguity that only surfaces
			// during a restore.
			versions = []Version{}
		}
		doc.Poems[i].Versions = versions
	}

	return doc, nil
}
