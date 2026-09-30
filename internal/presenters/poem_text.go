package presenters

import "strings"

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

func FlattenContent(content string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(content, "\n", " ")), " ")
}

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
