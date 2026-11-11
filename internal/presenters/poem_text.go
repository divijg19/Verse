package presenters

import (
	"strings"
	"unicode"
)

// FirstNonEmptyLine returns the first line of a work that is not blank.
//
// This is where a work's title comes from. The title is not a separate field: it is the work's
// first line, which is why the title and the body cannot drift apart, and why restoring an earlier
// revision restores its title with it.
func FirstNonEmptyLine(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// BodyWithoutTitle returns the work's content with its title line removed.
//
// The title is the work's first non-blank line, so a page that shows the title as a heading and
// the work as a body would show the title twice. This is the one place that knows how to take it
// out, and everything that needs a body uses it: the work page, the excerpt that goes into the feed
// and the search index, and the meta description.
//
// The blank lines that led up to the title go with it, so the body starts where the title ended
// rather than opening with a gap.
//
// The line endings are normalized the same way the title derivation normalizes them, so a work
// written on Windows does not carry a stray carriage return into the body.
func BodyWithoutTitle(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return strings.TrimLeft(strings.Join(lines[i+1:], "\n"), "\n")
	}

	// A work with no line in it at all has no title and no body.
	return ""
}

// FlattenContent collapses a work's line breaks into spaces, for a single-line summary.
func FlattenContent(content string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(content, "\n", " ")), " ")
}

// TruncateRunes shortens a string to at most max runes, marking the cut with an ellipsis.
//
// The cut is at an exact rune count, so it can land in the middle of a word. TruncateAtWord is the
// one to reach for when the text is prose.
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

// TruncateAtWord shortens a string to at most max runes, cutting on a word boundary.
//
// TruncateRunes cuts at an exact rune count, which can leave a fragment: "she reads the same pa".
// The reader is left with a broken word and an ellipsis, and the fragment says nothing at all.
// Cutting on the last whole word before the limit says the same thing and reads.
//
// The limit is a ceiling, not a target: the result is shorter than the limit whenever the word that
// straddles it is longer than the space left over, and the ellipsis is counted within the limit
// rather than added to it.
func TruncateAtWord(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}

	// The ellipsis is inside the limit, not added to it. Reserving the room first is what keeps
	// the result within the bound the caller asked for.
	limit := max - 3

	cut := limit
	for cut > 0 && !unicode.IsSpace(runes[cut-1]) {
		cut--
	}
	if cut == 0 {
		// The first word is longer than the limit. There is no word boundary to cut on, so the
		// exact limit is used rather than returning nothing at all.
		return string(runes[:limit]) + "..."
	}

	return strings.TrimRight(string(runes[:cut]), " \t\n") + "..."
}

// TitleWidths, the one place the per-surface title widths are named.
//
// Two widths, not one, and deliberately. A library row is wider than the dashboard's summary box, so
// a single width would either clip the box or leave the row ragged. The mismatch between a work's
// title on two surfaces is a layout decision, not the bug it looks like -- see
// TestTitleWidthsAreTheOneDifferenceBetweenSurfaces.
const (
	TitleWidthWide   = 80 // library rows, recycle rows, history entries
	TitleWidthNarrow = 72 // the dashboard's last-work summary
)

// WorkTitle derives the display title for a work, truncated to the given width.
//
// It is the same title the public site uses, with the same derivation, so the two cannot disagree
// about what a work is called.
//
// The three steps -- take the first line that is not blank, fall back when there is none, and elide
// to fit -- were written out five times across internal/handlers, at two widths, and three of the
// five remembered the fallback. One definition is the whole reason this function exists: the copies
// agreed today, and nothing said they had to.
//
// The fallback is unreachable through the application, because validateContent rejects a work that
// trims to nothing. It is kept because a row inserted by hand in SQL can be empty, and the records
// note that hand-editing is a real thing an author does.
func WorkTitle(content string, width int) string {
	title := FirstNonEmptyLine(content)
	if title == "" {
		title = "Untitled"
	}
	return TruncateRunes(title, width)
}
