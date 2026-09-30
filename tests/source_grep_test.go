package tests

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// errCannotLocateCaller is a named error rather than a formatted string, because runtime.Caller
// returning false is a programming-environment failure and a caller may want to test for it.
var errCannotLocateCaller = errors.New("cannot determine the calling file's path")

// sourceMatch is one hit from a repository-wide literal search.
type sourceMatch struct {
	file   string
	lineNo int
	line   string
}

// grepSourceLines finds a literal across the repository's Go and SQL files, skipping test files.
//
// Test files are skipped because every test that sets up a draft writes the publisher's predicate into
// its own SQL fixture -- the point of a fixture is to construct a specific row, and a shared constant
// cannot build four different arrangements. Including tests would make the guard report its own
// fixtures, and a guard that is always red is a guard that gets turned off.
//
// The skip is not "skip files whose name contains test", which would silently miss a helper package
// named testdata or a vendored test helper. It is the conventional Go suffix.
func grepSourceLines(needle string) ([]sourceMatch, error) {
	matches, _, _, err := grepSourceLinesCounted(needle)
	return matches, err
}

// grepSourceLinesCounted additionally reports how much source it looked at.
//
// Exists because a negative-result guard is otherwise impossible to write honestly. A caller asking
// "does the repository contain X" cannot distinguish "no" from "the search was broken": a pattern
// that matches nothing, a walk that stopped early, a path that no longer resolves. The counts let the
// caller prove the search happened over real files, which is the only thing separating a clean result
// from a silent one.
//
// Verified necessary, not speculative: TestAddingPublishedAtChangesNoExistingRead was written without
// it, and was caught passing on a needle matching nothing anywhere in the repository.
func grepSourceLinesCounted(needle string) (matches []sourceMatch, lines, files int, err error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return nil, 0, 0, errCannotLocateCaller
	}
	root := filepath.Join(filepath.Dir(thisFile), "..")
	self, err := filepath.Abs(thisFile)
	if err != nil {
		return nil, 0, 0, err
	}

	var out []sourceMatch
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Generated output, vendored trees and the untracked planning directory cannot contain
			// hand-written SQL, and walking them makes the check slower for no gain. dist/ is the
			// split's build output and is listed before it exists.
			switch info.Name() {
			case ".git", "node_modules", ".opencode", "vendor", "static", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		// This file necessarily contains the patterns it searches for, in string literals of its own,
		// so it would always find itself. Skipping it is the only honest option short of contorting the
		// pattern to avoid matching a literal that has to spell out what it matches.
		if abs, err := filepath.Abs(path); err == nil && abs == self {
			return nil
		}

		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".sql" {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		files++
		lines += strings.Count(string(body), "\n") + 1
		for i, line := range strings.Split(string(body), "\n") {
			// Comments are prose about the code, not the code. This is not a technicality: the
			// migration this release ships explains the publisher's filter in full, in a SQL comment,
			// and a guard that could not tell prose from an executable statement would have to exempt
			// that file -- which is precisely the exemption that would let a real second literal in it
			// go unnoticed.
			if isCommentLine(line) {
				continue
			}
			if strings.Contains(line, needle) {
				out = append(out, sourceMatch{file: rel, lineNo: i + 1, line: line})
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, lines, files, walkErr
	}
	return out, lines, files, nil
}

// isCommentLine reports whether a line is entirely a comment.
//
// Deliberately conservative: it only recognizes a line whose first non-space character begins a
// comment. A trailing comment on a line of code is not detected, which means a match there is
// reported and has to be looked at. That is the right way for this to be wrong. A more clever check
// that stripped trailing comments would suppress more findings, and the finding it suppressed could
// be a duplicated SQL predicate written as `... + " AND published_at IS NOT NULL"` on one line.
//
// A string literal containing "--" is also not detected, and that is the same trade: a false report
// costs one look, a missed predicate costs a draft on the public site.
func isCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "--")
}
