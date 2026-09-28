package tests

import (
	"bytes"
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/export"
	"github.com/divijg19/Verse/internal/testsupport"
	"github.com/divijg19/Verse/migrations"
)

// The migration this release ships, by name. Held as a constant so a rename fails here rather than
// silently skipping the file and leaving the test asserting nothing.
const timestamptzMigration = "006_timestamptz.sql"

// TestExportIsByteIdenticalAcrossTheTimestampMigration is the operator-facing guarantee for 006: the
// backup file does not change.
//
// Export is one-way -- there is no import path to round-trip through -- so "does converting the
// timestamps corrupt the backup" has exactly one honest answer available: serialize it, convert,
// serialize again, compare. Any difference is a change to a file the author may be relying on as the
// only copy of their work, and the difference would be silent unless something like this checks.
//
// The comparison is byte-for-byte rather than field-by-field on purpose. Field comparison is what an
// earlier export test does, and it passed straight through a change that altered how every timestamp
// in the file was written. Bytes are what the file is.
//
// The session is pinned to UTC, and that is not incidental. Once created_at is a timestamptz, pgx
// serializes it to JSON carrying the session's UTC offset, so an unpinned session would render "Z" on
// a UTC host and "+05:30" on any other, and the assertion would be vacuous in CI and noisy in
// production. Pinning the session is also what the application itself now does on every connection --
// see TestSessionZoneIsPinnedInProductionCode's counterpart in internal/database -- so the pinned case
// is the real one.
func TestExportIsByteIdenticalAcrossTheTimestampMigration(t *testing.T) {
	dsn := requireTestDSN(t)
	ctx := context.Background()

	schema := testsupport.NormalizeSchemaName("export_migration_t_", t.Name())
	scratch, cleanup, err := testsupport.ConnectScratchInZone(ctx, dsn, schema, "UTC")
	if err != nil {
		t.Fatalf("scratch schema: %v", err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("drop the scratch schema: %v", err)
		}
	})

	normalize := applyMigrationsExcept(t, scratch, timestamptzMigration)

	// Enough shape to be a real backup: several works, one soft-deleted so the optional timestamp is
	// exercised, and retained revisions so recorded_at travels too. The created_at values straddle
	// midnight, because that is where a zone shift shows up first -- see the log line at the end.
	seedTimestampsForMigration(t, scratch)

	// export.Build reads the package-level pool, so it is pointed at the scratch schema for the
	// duration and restored afterwards. The suite's tests are sequential -- there is no t.Parallel
	// anywhere in it -- so this swap cannot be observed by anything else.
	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	before := exportWithPinnedTimestamp(t)
	if _, err := scratch.Exec(ctx, normalize, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatalf("apply %s: %v", timestamptzMigration, err)
	}
	after := exportWithPinnedTimestamp(t)

	if bytes.Equal(before, after) {
		return
	}
	// Report the first divergence rather than two opaque blobs, since the whole failure is usually
	// one timestamp a few hundred bytes in.
	t.Errorf("the export changed across %s: %d bytes before, %d after\n  first difference at byte %d\n  before: %q\n  after:  %q",
		timestamptzMigration, len(before), len(after), firstDifference(before, after),
		windowAround(before, firstDifference(before, after)),
		windowAround(after, firstDifference(before, after)))
}

// exportWithPinnedTimestamp serializes the whole library, with the export's own clock fixed.
//
// ExportedAt is time.Now at the moment of the call, so two exports can never be equal without
// pinning it. Pinning one field rather than stripping it from the JSON keeps the assertion a true
// byte comparison of two complete files.
func exportWithPinnedTimestamp(t *testing.T) []byte {
	t.Helper()

	doc, err := export.Build(context.Background(), true)
	if err != nil {
		t.Fatalf("build the export: %v", err)
	}
	doc.ExportedAt = time.Unix(0, 0).UTC()

	body, err := export.JSON{}.Write(doc)
	if err != nil {
		t.Fatalf("serialize the export: %v", err)
	}
	return body
}

// applyMigrationsExcept applies every embedded migration except the named one, and returns the
// excluded file's SQL for the test to apply itself afterwards.
//
// The runner's own load is unexported, so the files are read from the embedded FS and applied here.
// That is a deliberate trade: this test is about what the migration does to stored data, and the
// runner's behavior around it is covered in internal/migrate. The alternative -- a second scratch
// schema left permanently at the old shape -- would drift from the real migrations and prove less.
func applyMigrationsExcept(t *testing.T, pool *pgxpool.Pool, except string) string {
	t.Helper()
	ctx := context.Background()

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read the embedded migrations: %v", err)
	}

	var held string
	for _, entry := range entries {
		body, err := fs.ReadFile(migrations.FS, entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if entry.Name() == except {
			held = string(body)
			continue
		}
		// Simple protocol: a migration may contain several statements, and the extended protocol
		// permits only one per execution. The runner does the same, for the same reason.
		if _, err := pool.Exec(ctx, string(body), pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatalf("apply %s: %v", entry.Name(), err)
		}
	}

	if held == "" {
		t.Fatalf("%s is not embedded; this test would assert that the export is stable because it "+
			"never converted anything", except)
	}
	return held
}

// seedTimestampsForMigration writes a small library whose timestamps sit on and around midnight UTC,
// so that a conversion which shifted any of them by a zone offset changes the day they belong to
// rather than merely the clock reading.
func seedTimestampsForMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Three instants chosen for what they straddle: 23:30 the day before, exactly midnight, and
	// 00:30 the day after. A five-and-a-half-hour shift in either direction moves all three onto
	// different calendar days.
	seeds := []struct {
		id        string
		createdAt string
		deleted   bool
	}{
		{"22222222-2222-2222-2222-222222222222", "2024-03-10 23:30:00", false},
		{"33333333-3333-3333-3333-333333333333", "2024-03-11 00:00:00", false},
		{"44444444-4444-4444-4444-444444444444", "2024-03-11 00:30:00", true},
	}

	for _, s := range seeds {
		if _, err := pool.Exec(ctx,
			`INSERT INTO poems (id, content, created_at) VALUES ($1, $2, $3::timestamp)`,
			s.id, "the work itself", s.createdAt); err != nil {
			t.Fatalf("insert poem %s: %v", s.id, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO poem_versions (id, poem_id, content, recorded_at)
			 VALUES ($1, $2, $3, $4::timestamp)`,
			"55555555-5555-5555-5555-55555555555"+s.id[0:1], s.id, "what it said before", s.createdAt); err != nil {
			t.Fatalf("insert revision for %s: %v", s.id, err)
		}
		if s.deleted {
			if _, err := pool.Exec(ctx,
				`UPDATE poems SET deleted_at = $2::timestamp WHERE id = $1`, s.id, s.createdAt); err != nil {
				t.Fatalf("soft-delete %s: %v", s.id, err)
			}
		}
	}
}

// firstDifference returns the index of the first differing byte, or the shorter length when one slice
// is a prefix of the other.
func firstDifference(a, b []byte) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

// windowAround returns a readable slice of body centered on at, for a failure message.
func windowAround(body []byte, at int) string {
	const radius = 48
	start := at - radius
	if start < 0 {
		start = 0
	}
	end := at + radius
	if end > len(body) {
		end = len(body)
	}
	return string(body[start:end])
}
