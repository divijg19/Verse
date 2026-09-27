package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// This file makes an invariant that was only ever prose actually enforced.
//
// The claim lived in a comment above truncatePoems: this is one of only two places in the repository
// that may issue a TRUNCATE, and a grep should return exactly two executable lines. By the time
// anyone counted, there were three -- the third added alongside the login rate limiter, with no
// thought of the comment.
//
// That is not a comment being slightly out of date. A TRUNCATE is the one statement in this codebase
// that can destroy the only copy of somebody's writing, and the property worth protecting is not the
// count but the gate: every one of them sits behind testsupport.DisposableDSN, which refuses to run
// unless the operator has named a disposable database and acknowledged the destructiveness
// explicitly. A new test that truncates without that check is a real hazard, and it is exactly the
// thing a comment cannot catch.

// executableTruncate matches a TRUNCATE inside a backtick literal.
//
// Backticks only, and single-line. Every SQL statement in this repository is a raw string, so
// backticks select exactly the statements, while a wider match also catches the double-quoted error
// *messages* that merely mention TRUNCATE -- the disposable-DSN gate explains itself in one -- and
// inflates the count until the log line is a lie about what was found.
//
// The \n exclusion matters too. A Go raw string may span lines, so an unconstrained `[^`]*` pairs
// whatever backtick comes before a TRUNCATE with whatever comes after, and a comment elsewhere in the
// same file is counted as a statement. Every statement here is one line, so the match is restricted
// to one.
var executableTruncate = regexp.MustCompile("`[^`\n]*TRUNCATE[^`\n]*`")

func TestEveryTruncateIsBehindTheDisposableGate(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine this file's path")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..")

	found := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// This file necessarily contains the pattern it searches for, in a backtick literal of its
		// own, so it would always find itself. Skipping it is the only honest option short of
		// contorting the pattern to avoid matching a regex that has to spell out what it matches.
		if same, _ := filepath.Abs(path); same == thisFile {
			return nil
		}
		if info.IsDir() {
			// Generated artifacts, vendored output and the untracked planning directory cannot
			// contain a hand-written TRUNCATE, and walking them makes the check slower for no gain.
			switch info.Name() {
			case ".git", "node_modules", ".opencode", "vendor", "static":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source := string(body)
		if !strings.Contains(source, "TRUNCATE") {
			return nil
		}

		// A file that gates its destructive work mentions DisposableDSN. This is a per-file check
		// rather than a per-statement one on purpose: the gate wraps the whole helper, so the
		// property is about the file being honest, not about one line's indentation.
		if !strings.Contains(source, "DisposableDSN") {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s issues a TRUNCATE but never consults testsupport.DisposableDSN.\n"+
				"  Every destructive statement in this repository must sit behind the disposable\n"+
				"  database gate, or it can run against a database nobody opted in to destroying.", rel)
		}
		found += len(executableTruncate.FindAllString(source, -1))
		return nil
	})
	if err != nil {
		t.Fatalf("walk the repository: %v", err)
	}

	// The count is asserted exactly, not as a floor. Three: the two poem truncates, one per test
	// package, and the login_attempts reset. An exact number is what makes a removal visible, and a
	// floor would happily pass with a statement deleted -- which is the drift this test exists to
	// catch, repeated in its own assertion.
	if found != 3 {
		t.Errorf("found %d executable TRUNCATE statement(s), want exactly 3.\n"+
			"  A fourth is a new destructive path that must be gated deliberately; a second is a\n"+
			"  removal that the comment above truncatePoems has not been told about.\n"+
			"  If the count is right and this is wrong, the pattern has stopped matching and the\n"+
			"  gate half of this test is vacuous too.", found)
	}
	t.Logf("found %d executable TRUNCATE statement(s), all behind the disposable gate", found)
}
