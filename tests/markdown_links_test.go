package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestTrackedMarkdownLinksResolve is the guard for a defect that shipped in v0.4.12.
//
// RUNNING.md was moved out of docs/ to the repository root, and a link to db/roles.sql kept its old
// `../db/roles.sql` path. It was correct from docs/ and wrong from the root, so the file the reader
// was told to run could not be opened by following the instruction -- in a release whose entire
// subject was that file.
//
// The move was mine, and the broken link was mine, in the same session as fixing two other
// documentation claims. There was no test that noticed, because nothing checked a link. There is a
// test that the README's project tree names only real paths, and a test that render.yaml's query
// count matches its measurement, and the seam between them is every prose link in every tracked
// document.
//
// Structural rather than a spot check, so it covers the next file and the next move: any relative
// link that does not resolve fails. Absolute URLs and bare anchors are skipped, as are links with no
// path component.
func TestTrackedMarkdownLinksResolve(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine this file's path")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..")

	// Inline links only. Reference-style definitions are not used in this repository, and a regex
	// that tried to resolve them would be guessing at Markdown rather than checking it.
	inline := regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

	// Only the files a reader is pointed at by name. CHANGELOG-style archives and vendored trees
	// are not in the repository, so nothing is excluded for that reason.
	docs := []string{"README.md", "RUNNING.md", "CONTRIBUTING.md"}

	totalChecked := 0
	for _, doc := range docs {
		t.Run(doc, func(t *testing.T) {
			path := filepath.Join(root, doc)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", doc, err)
			}

			checked := 0
			for _, match := range inline.FindAllStringSubmatch(string(body), -1) {
				target := match[1]

				// Absolute URLs, protocol-relative, and bare anchors are not files.
				if strings.HasPrefix(target, "http://") ||
					strings.HasPrefix(target, "https://") ||
					strings.HasPrefix(target, "//") ||
					strings.HasPrefix(target, "#") ||
					strings.HasPrefix(target, "mailto:") {
					continue
				}
				// A link with no path is a same-document reference.
				pathPart := target
				if i := strings.IndexAny(pathPart, "#"); i >= 0 {
					pathPart = pathPart[:i]
				}
				if pathPart == "" {
					continue
				}

				checked++
				resolved := filepath.Join(root, filepath.FromSlash(pathPart))
				if _, err := os.Stat(resolved); err != nil {
					t.Errorf("%s links to %q, which does not exist.\n"+
						"  Resolved from the repository root as %q.\n"+
						"  A reader who follows this instruction is told to do something the instruction\n"+
						"  does not make possible -- and a file that was moved is the usual reason.",
						doc, target, pathPart)
				}
			}

			totalChecked += checked
			t.Logf("%s: %d relative link(s) resolved", doc, checked)
		})
	}

	// Checked across all documents rather than per document, because a document with no links is
	// entirely normal -- CONTRIBUTING.md names files in prose rather than linking them -- while a
	// pattern that has stopped matching is not. This is the assertion that stops the test quietly
	// becoming a no-op after a regex or a file is edited.
	if totalChecked == 0 {
		t.Error("no relative markdown links were found in any tracked document.\n" +
			"  The pattern is no longer matching, so this test is passing without checking anything.")
	}
	t.Logf("%d relative link(s) checked across %d documents", totalChecked, len(docs))
}
