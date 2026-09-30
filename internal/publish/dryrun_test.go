package publish

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/models"
)

func ptr(t time.Time) *time.Time { return &t }

// TestTheDryRunTitleIsNotElided is a regression guard for a real design trap in this package.
//
// The obvious way to title a dry-run entry is presenters.WorkTitle -- the one function in the
// repository that turns content into a display title, and it truncates to a layout width. That would
// be wrong in a way that only shows up on a long first line: the author would be shown a work named
// "A Very Long Title That Keeps Going And Go" when the work is actually called something else, and
// the elision is invisible at exactly the point they are deciding whether to publish it.
//
// Same input, two widths. The untruncated length is asserted as well as the absence of an ellipsis,
// because a future change could truncate without using "...".
func TestTheDryRunTitleIsNotElided(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "x"
	}

	result, err := dryRun(context.Background(),
		func(context.Context) ([]models.Poem, error) {
			return []models.Poem{{ID: "one", Content: long, PublishedAt: ptr(time.Unix(1, 0))}}, nil
		},
		nil)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	if got := len(result.Entries[0].Title); got != 200 {
		t.Errorf("the dry-run elided a 200-character title to %d characters.\n"+
			"  A dry-run exists to report what a work is called; eliding it reports the layout\n"+
			"  width instead. Use presenters.FirstNonEmptyLine here, not WorkTitle.", got)
	}
	if result.Entries[0].Title != long {
		t.Error("the title does not match the content's first line")
	}
}

// TestTheDryRunReportsTheTitleAsWritten, including the case with no title at all.
//
// No "Untitled" fallback, unlike WorkTitle. A blank title in a dry-run is information: it means a row
// that is empty or whitespace, which validateContent should have prevented and which is therefore
// worth seeing. Silently naming it "Untitled" would hide the oddity the author is looking for, and the
// entry would then report a name the work does not have.
func TestTheDryRunReportsTheTitleAsWritten(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"first line", "A Title\n\nbody", "A Title"},
		{"leading blank lines are skipped", "\n\n   \nA Title\nbody", "A Title"},
		{"carriage returns are normalized", "A Title\r\nbody", "A Title"},
		{"no title at all stays empty", "\n\n   \n", ""},
		{"empty content stays empty", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := dryRun(context.Background(),
				func(context.Context) ([]models.Poem, error) {
					return []models.Poem{{ID: "one", Content: tc.content, PublishedAt: ptr(time.Unix(1, 0))}}, nil
				},
				nil)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if got := result.Entries[0].Title; got != tc.want {
				t.Errorf("title is %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTheDryRunReportsTheByteSize, not the rune count.
//
// The distinction is invisible for ASCII and wrong for everything else, and a byte count is what a
// reader downloads and what the static site will serve. The size is reported so the author can tell a
// library of four thousand notes from a library of twelve poems before deciding to publish it, and a
// rune count would under-report every non-Latin work by up to a factor of four.
func TestTheDryRunReportsTheByteSize(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"ascii", "hello"},
		{"multi-byte characters", "देवनागरी"}, // 5 characters, 21 bytes in UTF-8
		{"emoji", "🌍🌎🌏"},                      // 3 characters, 12 bytes
		{"mixed", "a देवनागरी 🌍 mix"},
		{"newlines count", "one\ntwo\n"},
		{"empty", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := dryRun(context.Background(),
				func(context.Context) ([]models.Poem, error) {
					return []models.Poem{{ID: "one", Content: tc.content, PublishedAt: ptr(time.Unix(1, 0))}}, nil
				},
				nil)
			if err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if got, want := result.Entries[0].SizeBytes, len(tc.content); got != want {
				t.Errorf("size is %d bytes, want %d for %q", got, want, tc.content)
			}
		})
	}
}

// TestTheDryRunDetectsATruncatingRead is what makes Result.Complete mean something.
//
// dryRun reads a list and a count from two separate calls and Complete compares them. The value is a
// scenario that cannot occur today: ListPublishedPoems has no limit, so they always agree. It is
// tested anyway, with a reader that deliberately returns fewer rows than it claims, because the
// scenario it guards is a plausible future change -- someone adding a LIMIT for performance -- and a
// check nobody has ever seen fail is a check nobody trusts when it does.
func TestTheDryRunDetectsATruncatingRead(t *testing.T) {
	all := []models.Poem{
		{ID: "one", Content: "first", PublishedAt: ptr(time.Unix(1, 0))},
		{ID: "two", Content: "second", PublishedAt: ptr(time.Unix(2, 0))},
		{ID: "three", Content: "third", PublishedAt: ptr(time.Unix(3, 0))},
	}
	honest := func(context.Context) (int, error) { return 3, nil }

	truncated, err := dryRun(context.Background(),
		func(context.Context) ([]models.Poem, error) { return all[:2], nil },
		honest)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if truncated.Complete() {
		t.Errorf("the dry-run reported itself complete while showing %d of %d published works.\n"+
			"  A silent truncation here becomes a static site that looks complete and is not.",
			truncated.Count, truncated.Counted)
	}
	if truncated.Count != 2 || truncated.Counted != 3 {
		t.Errorf("expected 2 entries and a count of 3, got %d and %d", truncated.Count, truncated.Counted)
	}

	full, err := dryRun(context.Background(),
		func(context.Context) ([]models.Poem, error) { return all, nil },
		honest)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !full.Complete() {
		t.Error("an untruncated read was reported as incomplete")
	}
}

// TestTheDryRunSurvivesANilPublishedAt covers the one field that is nil-able on the way in.
//
// Every published work has a published_at, because the filter requires it -- so a nil here means the
// filter was bypassed, and reading it naively would panic. A zero timestamp is the safe rendering: it
// is visibly wrong rather than a crash, and a caller can notice it.
func TestTheDryRunSurvivesANilPublishedAt(t *testing.T) {
	result, err := dryRun(context.Background(),
		func(context.Context) ([]models.Poem, error) {
			return []models.Poem{{ID: "one", Content: "work"}}, nil // PublishedAt deliberately nil
		},
		nil)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !result.Entries[0].PublishedAt.IsZero() {
		t.Errorf("expected a zero timestamp for a nil published_at, got %v", result.Entries[0].PublishedAt)
	}
}

// TestTheDryRunIsEmptyForAnEmptyLibrary. The first build of the split's reader emits an empty, valid
// site, and an empty result must be an empty result rather than a nil slice that prints as "null" or
// an error.
func TestTheDryRunIsEmptyForAnEmptyLibrary(t *testing.T) {
	result, err := dryRun(context.Background(),
		func(context.Context) ([]models.Poem, error) { return nil, nil },
		nil)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result.Count != 0 || result.Counted != 0 {
		t.Errorf("expected an empty result, got count %d and counted %d", result.Count, result.Counted)
	}
	if !result.Complete() {
		t.Error("an empty library was reported as incomplete")
	}
	// Non-nil, so a caller can range over it and so it serializes as [] rather than null.
	if result.Entries == nil {
		t.Error("Entries is nil; an empty dry-run should still be a usable empty slice")
	}
}

// TestTheDryRunPropagatesReadAndCountErrors. A dry-run that reports an empty library because its query
// failed is worse than one that fails, because an empty library looks like a correct answer.
func TestTheDryRunPropagatesReadAndCountErrors(t *testing.T) {
	wantRead := errors.New("read failed")
	if _, err := dryRun(context.Background(),
		func(context.Context) ([]models.Poem, error) { return nil, wantRead },
		nil); !errors.Is(err, wantRead) {
		t.Errorf("expected the read error to propagate, got %v", err)
	}

	wantCount := errors.New("count failed")
	if _, err := dryRun(context.Background(),
		func(context.Context) ([]models.Poem, error) { return nil, nil },
		func(context.Context) (int, error) { return 0, wantCount }); !errors.Is(err, wantCount) {
		t.Errorf("expected the count error to propagate, got %v", err)
	}
}
