package seed

import (
	"strings"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/presenters"
)

// firstLine returns the first non-blank line of a body, which is what the title is taken from.
func firstLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func TestGenerateIsDeterministicForASeed(t *testing.T) {
	opts := Options{Seed: 7, Count: 30, Published: 5, Deleted: 3, Now: time.Now()}

	first := Generate(opts)
	second := Generate(opts)

	if len(first) != len(second) {
		t.Fatalf("the same seed produced %d works and then %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Content != second[i].Content {
			t.Fatalf("work %d differs between two runs of the same seed", i)
		}
		if !first[i].CreatedAt.Equal(second[i].CreatedAt) {
			t.Fatalf("work %d was written at a different time between two runs of the same seed", i)
		}
	}
}

func TestGenerateDiffersBetweenSeeds(t *testing.T) {
	opts := Options{Count: 30, Published: 5, Deleted: 3, Now: time.Now()}

	first := Generate(opts)

	other := opts
	other.Seed = 8
	second := Generate(other)

	same := 0
	for i := range first {
		if first[i].Content == second[i].Content {
			same++
		}
	}
	if same == len(first) {
		t.Fatal("two different seeds produced an identical library")
	}
}

func TestGenerateWritesTheRequestedCounts(t *testing.T) {
	works := Generate(Options{
		Seed:      3,
		Count:     40,
		Published: 7,
		Deleted:   5,
		Now:       time.Now(),
	})

	if len(works) != 40 {
		t.Fatalf("generated %d works, want 40", len(works))
	}

	published, deleted := 0, 0
	for i, work := range works {
		if work.PublishedAt != nil {
			published++
		}
		if work.DeletedAt != nil {
			deleted++
		}
		if work.PublishedAt != nil && work.DeletedAt != nil {
			t.Fatalf("work %d is both published and deleted, a state the author cannot reach", i)
		}
	}

	if published != 7 {
		t.Fatalf("published %d works, want 7", published)
	}
	if deleted != 5 {
		t.Fatalf("deleted %d works, want 5", deleted)
	}
}

func TestGeneratedWorksHaveATitle(t *testing.T) {
	works := Generate(Options{Seed: 11, Count: 50, Published: 5, Deleted: 5, Now: time.Now()})

	for i, work := range works {
		title := firstLine(work.Content)
		if title == "" {
			t.Fatalf("work %d has no first line, so it has no title", i)
		}
		if got := presenters.WorkTitle(work.Content, 80); got != title && len(title) <= 80 {
			t.Fatalf("work %d renders a title of %q, want %q", i, got, title)
		}
	}
}

func TestGeneratedWorksSpanTheWindowAndEndRecently(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	works := Generate(Options{Seed: 5, Count: 60, Published: 10, Deleted: 6, Now: now, Days: 90})

	oldest := now
	for i, work := range works {
		if work.CreatedAt.After(now) {
			t.Fatalf("work %d is dated in the future: %s", i, work.CreatedAt)
		}
		if work.CreatedAt.Before(oldest) {
			oldest = work.CreatedAt
		}
	}

	// The window is reached, so the heatmap has something in it.
	if oldest.After(now.AddDate(0, 0, -80)) {
		t.Fatalf("the oldest work is %s, which does not reach back through the window", oldest)
	}

	// The most recent days carry work, so the streak is not a length of one.
	today := now.UTC().Truncate(24 * time.Hour)
	recent := 0
	for _, work := range works {
		day := work.CreatedAt.UTC().Truncate(24 * time.Hour)
		if !day.Before(today.AddDate(0, 0, -3)) {
			recent++
		}
	}
	if recent < 4 {
		t.Fatalf("only %d works fall in the last three days, so the streak reads as one", recent)
	}
}

func TestRevisionsPrecedeTheWorkTheyBelongTo(t *testing.T) {
	works := Generate(Options{Seed: 13, Count: 60, Published: 5, Deleted: 5, Now: time.Now()})

	withVersions := 0
	for i, work := range works {
		for j, revision := range work.Versions {
			withVersions++
			if !revision.RecordedAt.Before(work.CreatedAt) {
				t.Fatalf("work %d revision %d was recorded at %s, which is not before the work at %s",
					i, j, revision.RecordedAt, work.CreatedAt)
			}
			if firstLine(revision.Content) == "" {
				t.Fatalf("work %d revision %d has no first line", i, j)
			}
		}
	}
	if withVersions == 0 {
		t.Fatal("no work carries a revision, so the history and restore paths have nothing to act on")
	}
}

func TestOptionsFillTheirDefaults(t *testing.T) {
	opts := Options{}.WithDefaults()

	if opts.Count != DefaultCount {
		t.Fatalf("Count = %d, want %d", opts.Count, DefaultCount)
	}
	// The published and deleted shares are derived from the count, so the library keeps its shape
	// when the count changes.
	if opts.Published != DefaultCount/publishedRatio {
		t.Fatalf("Published = %d, want %d", opts.Published, DefaultCount/publishedRatio)
	}
	if opts.Deleted != DefaultCount/deletedRatio {
		t.Fatalf("Deleted = %d, want %d", opts.Deleted, DefaultCount/deletedRatio)
	}
	if opts.Days != DefaultDays {
		t.Fatalf("Days = %d, want %d", opts.Days, DefaultDays)
	}
	if opts.Now.IsZero() {
		t.Fatal("Now was left at the zero time")
	}
}

func TestOptionsDeriveTheSharesFromTheCount(t *testing.T) {
	opts := Options{Count: 120}.WithDefaults()

	if opts.Published != 20 {
		t.Fatalf("Published = %d, want 20", opts.Published)
	}
	if opts.Deleted != 12 {
		t.Fatalf("Deleted = %d, want 12", opts.Deleted)
	}
}

func TestOptionsTakeNoneForANegativeShare(t *testing.T) {
	opts := Options{Count: 40, Published: -1, Deleted: -1}.WithDefaults()

	if opts.Published != 0 {
		t.Fatalf("Published = %d, want 0", opts.Published)
	}
	if opts.Deleted != 0 {
		t.Fatalf("Deleted = %d, want 0", opts.Deleted)
	}
}

func TestGenerateProducesNothingForANegativeCount(t *testing.T) {
	if works := Generate(Options{Count: -1}); len(works) != 0 {
		t.Fatalf("generated %d works for a count of none", len(works))
	}
}

func TestRequireLocalDSNAcceptsThisMachine(t *testing.T) {
	for _, dsn := range []string{
		"postgres://verse:verse@localhost:5432/verse?sslmode=disable",
		"postgres://verse:verse@127.0.0.1:5432/verse?sslmode=disable",
	} {
		if err := RequireLocalDSN(dsn); err != nil {
			t.Errorf("RequireLocalDSN(%q) = %v, want nil", dsn, err)
		}
	}
}

func TestRequireLocalDSNRefusesAHostedDatabase(t *testing.T) {
	for _, dsn := range []string{
		"postgres://verse:secret@db.example.com:5432/verse?sslmode=require",
		"postgres://verse:secret@10.0.0.4:5432/verse?sslmode=require",
	} {
		err := RequireLocalDSN(dsn)
		if err == nil {
			t.Errorf("RequireLocalDSN(%q) = nil, want a refusal", dsn)
			continue
		}
		if !strings.Contains(err.Error(), "not on this machine") {
			t.Errorf("RequireLocalDSN(%q) = %v, want a refusal that says why", dsn, err)
		}
	}
}

func TestRequireLocalDSNCanBeOverridden(t *testing.T) {
	t.Setenv(ForceRemoteEnv, "1")

	if err := RequireLocalDSN("postgres://verse:secret@db.example.com:5432/verse"); err != nil {
		t.Fatalf("RequireLocalDSN with the override = %v, want nil", err)
	}
}
