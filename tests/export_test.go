package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/export"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/internal/testsupport"
)

// This file covers export, which exists because the work had no way out of the application.
//
// The guarantee is losslessness, and the tests here are written to break it. TestExportRoundTripsEvery
// PoemByteForByte is the one that matters: it compares an export against the database character by
// character, over content chosen to break the obvious ways a text format loses things -- embedded
// newlines, quotes, backslashes, angle brackets, tabs, emoji outside the basic plane, leading and
// trailing whitespace, and a poem that is only whitespace.
//
// "It looks right" is not a property an export can have. Either every byte survives or the archive
// is a lossy summary of the thing it claims to preserve.

// adversarialContent is the corpus the round-trip test runs over. Each entry would be damaged by a
// careless encoder.
var adversarialContent = []string{
	"a plain line",
	"line one\nline two\nline three",
	"windows\r\nline endings\r\n",
	"a lone carriage return\rreturn",
	`quotes "double" and 'single'`,
	`a backslash \ and a percent % and a dollar $`,
	"angle < brackets > and an ampersand &",
	"tabs\tand\ttabs",
	"  leading and trailing whitespace  ",
	"\n\nleading newlines",
	"trailing newlines\n\n\n",
	"emoji outside the basic plane: 🌙✍️🎋",
	"combining marks: éá and RTL: אב",
	"a ``` fenced block ``` inside a poem",
	"``` exactly three backticks",
	"a very long " + strings.Repeat("line of verse ", 500),
	"null-ish text: NULL and \\x00 as literal words",
	"   ",
	"\n",
}

// TestExportRoundTripsEveryPoemByteForByte is the correctness claim of the whole feature.
func TestExportRoundTripsEveryPoemByteForByte(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	// One poem per adversarial case, then edit each so every one also has a retained revision.
	for _, content := range adversarialContent {
		id := insertPoem(t, content)
		edited := content + "\n[edited]"
		if err := services.UpdatePoem(context.Background(), id, edited); err != nil {
			t.Fatalf("update: %v", err)
		}
	}

	doc := buildExport(t, true)
	byContent := map[string]export.Poem{}
	for _, p := range doc.Poems {
		// Keyed by id rather than content: two adversarial cases could collide, and a collision
		// would let one poem stand in for another and hide a loss.
		byContent[p.ID] = p
	}

	// Rebuild the expected mapping straight from the database.
	want := map[string]struct {
		content   string
		versions  []string
		deletedAt *time.Time
	}{}

	rows, err := database.Pool.Query(context.Background(),
		`SELECT id, content, created_at, deleted_at FROM poems ORDER BY id`)
	if err != nil {
		t.Fatalf("read poems: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, content string
		var createdAt time.Time
		var deletedAt *time.Time
		if err := rows.Scan(&id, &content, &createdAt, &deletedAt); err != nil {
			t.Fatalf("scan poem: %v", err)
		}
		want[id] = struct {
			content   string
			versions  []string
			deletedAt *time.Time
		}{content: content, versions: versionContents(t, id), deletedAt: deletedAt}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate poems: %v", err)
	}

	if len(doc.Poems) != len(want) {
		t.Fatalf("the export has %d poem(s), the database has %d; an export that omits work is not a backup",
			len(doc.Poems), len(want))
	}

	for id, expected := range want {
		got, ok := byContent[id]
		if !ok {
			t.Fatalf("poem %s is missing from the export", id)
		}
		if got.Content != expected.content {
			t.Errorf("poem %s content round-tripped to a different value.\n  want %q\n  got  %q",
				id, expected.content, got.Content)
		}
		if len(got.Versions) != len(expected.versions) {
			t.Fatalf("poem %s has %d retained revision(s) in the export, want %d",
				id, len(got.Versions), len(expected.versions))
		}
		for i, wantVersion := range expected.versions {
			if got.Versions[i].Content != wantVersion {
				t.Errorf("poem %s revision %d round-tripped to a different value.\n  want %q\n  got  %q",
					id, i, wantVersion, got.Versions[i].Content)
			}
		}
	}
}

// TestExportSurvivesAJSONRoundTrip parses the encoded bytes back, rather than trusting the struct
// that produced them. An encoder that mangles a value on the way out is invisible to any test that
// inspects the struct.
func TestExportSurvivesAJSONRoundTrip(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	const awkward = "quotes \" backslash \\ angle < amp & newline\ntab\t emoji 🌙"
	id := insertPoem(t, awkward)
	if err := services.UpdatePoem(context.Background(), id, awkward+"\nsecond"); err != nil {
		t.Fatalf("update: %v", err)
	}

	body, err := export.JSON{}.Write(buildExport(t, true))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// The encoder must not be HTML-escaping. With escaping on -- the encoding/json default -- the
	// text is written as the < and & escapes instead, so a human reading the archive sees noise and
	// a naive tool can read it as corruption. Escaping off means the characters appear literally and
	// the escapes do not appear at all.
	if strings.Contains(string(body), `\u003c`) || strings.Contains(string(body), `\u0026`) {
		t.Errorf("the export HTML-escaped its content, which alters the work on the way out:\n%s",
			truncate(body))
	}
	if !strings.Contains(string(body), "angle < amp &") {
		t.Error("the export did not carry the angle bracket and ampersand through literally")
	}

	var parsed export.Document
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("the export is not valid JSON: %v", err)
	}
	if parsed.Format != export.Format {
		t.Errorf("format tag = %q, want %q", parsed.Format, export.Format)
	}
	if parsed.Version != export.FormatVersion {
		t.Errorf("format version = %d, want %d", parsed.Version, export.FormatVersion)
	}
	if len(parsed.Poems) != 1 {
		t.Fatalf("the export has %d poem(s), want 1", len(parsed.Poems))
	}
	if got := parsed.Poems[0].Content; got != awkward+"\nsecond" {
		t.Errorf("content after a JSON round trip:\n  want %q\n  got  %q", awkward+"\nsecond", got)
	}
	if len(parsed.Poems[0].Versions) != 1 || parsed.Poems[0].Versions[0].Content != awkward {
		t.Errorf("the retained revision did not survive the round trip: %+v", parsed.Poems[0].Versions)
	}
	// A poem with no history must still carry the field, so "no history" and "field absent" stay
	// distinguishable to whatever reads this back.
	if parsed.Poems[0].Versions == nil {
		t.Error("versions serialized as null rather than an empty list")
	}
}

// TestExportedTextIsRestorableWrites the export to disk and reads it back, because an export nobody
// can open is not a backup.
func TestExportedTextIsRestorable(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	insertPoem(t, "a work to keep")

	body, err := export.JSON{}.Write(buildExport(t, true))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	path := filepath.Join(t.TempDir(), "verse.json")
	// 0o600: the export is the entire body of work, and a world-readable copy of it in a temp
	// directory is the same mistake the application exists to avoid.
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	readBack, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(readBack) != string(body) {
		t.Fatal("the export did not survive being written to disk and read back")
	}
}

// TestExportExcludesDeletedUnlessAsked: a routine export is the current work. The command defaults to
// excluding, and the tests pin that the switch works.
func TestExportExcludesDeletedUnlessAsked(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	kept := insertPoem(t, "still here")
	gone := insertPoem(t, "deleted work")
	if err := services.SoftDeletePoem(context.Background(), gone); err != nil {
		t.Fatalf("delete: %v", err)
	}

	without := buildExport(t, false)
	if len(without.Poems) != 1 || without.Poems[0].ID != kept {
		t.Fatalf("an export excluding deleted work returned %d poem(s), want only the live one",
			len(without.Poems))
	}

	with := buildExport(t, true)
	if len(with.Poems) != 2 {
		t.Fatalf("a full export returned %d poem(s), want 2", len(with.Poems))
	}
	for _, p := range with.Poems {
		if p.ID == gone && p.DeletedAt == nil {
			t.Error("a deleted work was exported without recording that it is deleted")
		}
	}
}

// TestExportRouteRequiresASession: the export is the entire library, so serving it publicly would
// publish everything.
func TestExportRouteRequiresASession(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	insertPoem(t, "private work")

	srv := newLoginServer(t, true)
	c := newAnonymousClient(t, srv)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp := doGet(t, c, srv.URL+"/export")
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Fatal("the export was served without a session; it is the entire body of work")
	}
}

// TestExportRouteServesADownload pins the headers, since a browser that treats the response as a page
// rather than a file is a broken backup path.
func TestExportRouteServesADownload(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	insertPoem(t, "a work to keep")

	srv := newTestServer(t)

	status, body, headers := get(t, srv.URL+"/export", nil)
	if status != 200 {
		t.Fatalf("GET /export = %d, want 200; body: %s", status, truncate([]byte(body)))
	}

	if cd := headers.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	} else if !strings.Contains(cd, "verse-") || !strings.Contains(cd, ".json") {
		t.Errorf("Content-Disposition = %q, want a dated verse-*.json filename", cd)
	}
	if got := headers.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store; an export must not be cached in between", got)
	}
	if got := headers.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	// And the body must be the export, not a login page.
	var parsed export.Document
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("the download is not a valid export: %v", err)
	}
	if len(parsed.Poems) != 1 || parsed.Poems[0].Content != "a work to keep" {
		t.Errorf("the download does not carry the work: %+v", parsed.Poems)
	}
}

// TestExportRouteRejectsAnUnknownFormat fails loudly rather than serving something unexpected.
func TestExportRouteRejectsAnUnknownFormat(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)

	status, _, _ := get(t, srv.URL+"/export?format=csv", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("GET /export?format=csv = %d, want 400", status)
	}
}

// TestMarkdownExportIsReadable checks the human-facing format does not mangle a poem containing a
// code fence, which is the one thing it could plausibly get wrong.
func TestMarkdownExportIsReadable(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	insertPoem(t, "a poem with ``` three backticks ``` inside")
	insertPoem(t, "an ordinary poem")

	body, err := export.Markdown{}.Write(buildExport(t, true))
	if err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	out := string(body)
	if !strings.Contains(out, "a poem with ``` three backticks ``` inside") {
		t.Errorf("the markdown export altered a poem containing a code fence:\n%s", truncate([]byte(out)))
	}
	if !strings.Contains(out, "an ordinary poem") {
		t.Error("the markdown export omitted a poem")
	}
}

// buildExport builds an export against the connected test database.
func buildExport(t *testing.T, includeDeleted bool) export.Document {
	t.Helper()
	doc, err := export.Build(context.Background(), includeDeleted)
	if err != nil {
		t.Fatalf("build export: %v", err)
	}
	return doc
}

// versionContents returns a poem's retained revision contents, oldest first.
func versionContents(t *testing.T, poemID string) []string {
	t.Helper()
	versions, err := services.ListPoemVersions(context.Background(), poemID)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	// ListPoemVersions is newest first; the export is oldest first.
	out := make([]string, 0, len(versions))
	for i := len(versions) - 1; i >= 0; i-- {
		out = append(out, versions[i].Content)
	}
	return out
}

// countingTracer records how many queries a pool issues.
type countingTracer struct {
	mu      sync.Mutex
	queries []string
}

func (c *countingTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, data.SQL)
	return ctx
}

func (c *countingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *countingTracer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queries)
}

// TestExportIssuesAConstantNumberOfQueries is the claim that v0.4.4's export could not support.
//
// Build issued one query per poem to collect its retained revisions. At ten thousand works that is
// ten thousand and one round trips inside a single 30s request, so the export would have timed out on
// a library a tenth of that size. Correctness was not the problem; the shape of the query was.
//
// Asserting the count rather than the output is the point: the round-trip test above already proves
// the export is faithful, and it would keep passing if this regressed.
func TestExportIssuesAConstantNumberOfQueries(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	dsn, reason := testsupport.DisposableDSN()
	if reason != "" {
		t.Skip(reason)
	}

	// A pool of the test's own, with a tracer attached, swapped in for the duration. The tracer is
	// the only way to observe the shape of the access rather than inferring it from a duration,
	// which would be slow and would not distinguish one query from fifty on a fast machine.
	scoped, err := testsupport.WithSearchPath(dsn, packageSchema)
	if err != nil {
		t.Fatalf("scope dsn: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(scoped)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	tracer := &countingTracer{}
	cfg.ConnConfig.Tracer = tracer

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect traced pool: %v", err)
	}
	t.Cleanup(pool.Close)

	original := database.Pool
	database.Pool = pool
	t.Cleanup(func() { database.Pool = original })

	// Seed, so the count is measured over real data rather than an empty table.
	const works = 40
	for i := 0; i < works; i++ {
		id := insertPoem(t, "work "+strconv.Itoa(i))
		if err := services.UpdatePoem(context.Background(), id, "work "+strconv.Itoa(i)+" edited"); err != nil {
			t.Fatalf("edit %d: %v", i, err)
		}
	}

	tracer.mu.Lock()
	tracer.queries = nil
	tracer.mu.Unlock()

	doc, err := export.Build(context.Background(), true)
	if err != nil {
		t.Fatalf("build export: %v", err)
	}
	if len(doc.Poems) != works {
		t.Fatalf("export has %d poem(s), want %d", len(doc.Poems), works)
	}

	queries := tracer.count()
	t.Logf("exporting %d works with %d retained revisions took %d query/queries",
		works, works, queries)

	// Two: the poems, and the revisions. The old shape was one plus one per work, so 41 here and 41
	// for a library of any size. A small ceiling rather than an exact figure, so a future extra
	// lookup does not fail this for the wrong reason.
	if queries > 4 {
		t.Errorf("building an export of %d works issued %d queries; the revisions must be read in "+
			"one pass, not one query per work", works, queries)
	}
}
