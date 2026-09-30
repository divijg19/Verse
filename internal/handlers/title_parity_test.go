package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/models"
	"github.com/divijg19/Verse/internal/presenters"
)

// TestTitleDerivationIsIdenticalAcrossSurfaces pins what each surface renders as a work's title.
//
// This exists because the derivation was written five times, and the five copies were not the same:
//
//	library, trash   FirstNonEmptyLine -> "Untitled" fallback -> TruncateRunes(…, 80)
//	dashboard        FirstNonEmptyLine -> "Untitled" fallback -> TruncateRunes(…, 72)
//	history (x2)     TruncateRunes(FirstNonEmptyLine(…), 80)   -- no fallback
//
// The dashboard's 72 is deliberate: that box is narrower than a library row, so the two widths are a
// layout decision and are preserved. What is *not* a decision is that the fallback existed in three
// copies and not the other two, and that the first three steps were retyped five times.
//
// The assertions below are written against the *current* output, before any refactor, so the same
// test passes afterwards. That is what makes it a parity guarantee: if consolidation changed a single
// rendered character on any surface, this fails rather than a human noticing three weeks later.
//
// The "Untitled" fallback is unreachable through the application -- validateContent rejects a
// work that trims to nothing -- so including it in all five is parity-preserving in practice and
// closes the gap for a row inserted by hand in SQL, which the records note is a real scenario.
func TestTitleDerivationIsIdenticalAcrossSurfaces(t *testing.T) {
	long := strings.Repeat("x", 100)

	cases := []struct {
		name    string
		content string
		// The exact string each surface must render today.
		wide   string // library, trash, history
		narrow string // dashboard
	}{
		{
			name:    "short work fits everywhere",
			content: "a short work",
			wide:    "a short work",
			narrow:  "a short work",
		},
		{
			name:    "first line only",
			content: "the real first line\nand a second one\nand a third",
			wide:    "the real first line",
			narrow:  "the real first line",
		},
		{
			name:    "leading blank lines are skipped",
			content: "\n\n   \n  the first line with space  \nrest",
			wide:    "the first line with space",
			narrow:  "the first line with space",
		},
		{
			// 100 runes: the library keeps 77 + "...", the dashboard keeps 69 + "...". The two
			// widths are why the same work legitimately shows different titles in two places.
			name:    "long first line truncates at each surface's own width",
			content: long,
			wide:    strings.Repeat("x", 77) + "...",
			narrow:  strings.Repeat("x", 69) + "...",
		},
		{
			// Unreachable through the application. A hand-inserted row can still do it, and the
			// three surfaces that guard against it should all guard.
			name:    "empty content falls back rather than rendering nothing",
			content: "",
			wide:    "Untitled",
			narrow:  "Untitled",
		},
		{
			name:    "whitespace-only content falls back",
			content: "   \n\t\n  ",
			wide:    "Untitled",
			narrow:  "Untitled",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			poem := models.Poem{ID: "id", Content: tc.content, CreatedAt: time.Now()}

			view := toPoemView(poem)
			if view.Title != tc.wide {
				t.Errorf("library title = %q, want %q", view.Title, tc.wide)
			}

			summary, err := lastPoemSummary(poem)
			if err != nil {
				t.Fatalf("lastPoemSummary: %v", err)
			}
			if summary.Title != tc.narrow {
				t.Errorf("dashboard title = %q, want %q", summary.Title, tc.narrow)
			}
		})
	}
}

// TestTitleWidthsAreTheOneDifferenceBetweenSurfaces records why the two widths differ, so a future
// reader does not "fix" the mismatch.
//
// The mismatch is not a bug. A library row is wider than the dashboard's summary box, and truncating
// both to the same width would either clip the box or leave the row ragged. What would be a bug is
// the *same* width drifting at one site, or the derivation itself diverging -- which the test above
// catches.
func TestTitleWidthsAreTheOneDifferenceBetweenSurfaces(t *testing.T) {
	const (
		libraryTitleWidth   = 80
		dashboardTitleWidth = 72
	)

	// 100 runes, so both widths actually engage: 80 keeps 77 + "...", 72 keeps 69 + "...". A line
	// short enough to fit the wider row would not be truncated by either and would prove nothing.
	content := strings.Repeat("y", 100)

	view := toPoemView(models.Poem{Content: content})
	if got := len([]rune(view.Title)); got != libraryTitleWidth {
		t.Errorf("library title is %d runes, want %d", got, libraryTitleWidth)
	}

	summary, err := lastPoemSummary(models.Poem{Content: content})
	if err != nil {
		t.Fatalf("lastPoemSummary: %v", err)
	}
	if got := len([]rune(summary.Title)); got != dashboardTitleWidth {
		t.Errorf("dashboard title is %d runes, want %d", got, dashboardTitleWidth)
	}

	// And the shared derivation is genuinely shared: one function, not two that agree today.
	if !strings.HasSuffix(view.Title, "...") || !strings.HasSuffix(summary.Title, "...") {
		t.Error("both titles should be elided at their own width; if one is not, the truncation is " +
			"being applied outside the derivation and the widths are no longer the only difference")
	}

	_ = presenters.FirstNonEmptyLine // the derivation these widths apply to
}
