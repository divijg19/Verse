package templ

import (
	"strings"
	"testing"
)

// TestBodyRemovesTheTitleLine checks the split between a work's title and its body.
//
// The title is the work's first non-blank line, so a page that shows the title as a heading and the
// work as a body has to take that line out of the body once. Otherwise the reader sees the title
// twice, which is what the work page did before this was fixed.
func TestBodyRemovesTheTitleLine(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "a title and a body",
			content: "The First Work\n\nA body that belongs to the first.",
			want:    "A body that belongs to the first.",
		},
		{
			name:    "a title with no body",
			content: "The First Work",
			want:    "",
		},
		{
			name:    "blank lines before the title",
			content: "\n\nThe First Work\n\nA body.",
			want:    "A body.",
		},
		{
			name:    "a title and a body with no blank line between",
			content: "The First Work\nA body.",
			want:    "A body.",
		},
		{
			name:    "a body with blank lines inside it",
			content: "The First Work\n\nFirst stanza\n\nSecond stanza",
			want:    "First stanza\n\nSecond stanza",
		},
		{
			name:    "nothing at all",
			content: "",
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work := PublicWork{Content: tc.content}

			if got := work.Body(); got != tc.want {
				t.Errorf("Body() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBodyAndTitleTogetherAreTheWholeWork checks that the split loses nothing.
//
// The title and the body are two views of one work, and a reader who puts them back together has
// to get the work they wrote.
//
// The title is the trimmed first non-blank line, so the rejoin uses the trimmed form. A line the
// author indented is the same line whatever the indentation was.
func TestBodyAndTitleTogetherAreTheWholeWork(t *testing.T) {
	content := "The First Work\n\nA body that belongs to the first."
	work := PublicWork{Content: content, Title: "The First Work"}

	rejoined := work.Title + "\n\n" + work.Body()
	if rejoined != content {
		t.Errorf("the title and body together are %q, want %q", rejoined, content)
	}
}

// TestBodyNormalizesLineEndings checks the carriage returns.
//
// A work written on Windows arrives with carriage returns on its lines. The title derivation
// normalizes them, so the body has to as well, or a Windows work and a Unix work render
// differently.
func TestBodyNormalizesLineEndings(t *testing.T) {
	work := PublicWork{Content: "The First Work\r\n\r\nA body.\r\n"}

	if got := work.Body(); got != "A body.\n" {
		t.Errorf("Body() = %q, want %q", got, "A body.\n")
	}
	if strings.ContainsRune(work.Body(), '\r') {
		t.Error("Body() kept a carriage return")
	}
}
