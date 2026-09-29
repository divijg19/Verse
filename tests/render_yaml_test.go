package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestRenderYamlDashboardQueryCountMatchesTheMeasurement holds render.yaml to the number the pool
// test measures.
//
// The comment in render.yaml tells an operator that probing "/" would cost them a full dashboard
// render, and gives a figure for it. That figure was written by hand and was wrong the moment the
// dashboard was collapsed from four queries to two, which means the one file an operator reads to
// understand the health check was describing a version of the application that no longer existed.
// A wrong comment is worse than no comment, because it is trusted.
//
// This test cannot run the dashboard, so it does the next best thing: it reads the documented number
// and requires it to be the same number the measurement asserts. The measurement lives in
// TestDashboardLoadLeavesTheHealthProbeAConnection. If the dashboard's query count changes, that
// test fails first and its failure message names the new count; this test then fails because the two
// disagree. Neither can drift alone.
//
// The two facts are deliberately kept on either side of the boundary: the count is measured here and
// asserted there, and only the documentation is read here. Nothing in this file can make a dashboard
// load cheaper or more expensive.

func TestRenderYamlDashboardQueryCountMatchesTheMeasurement(t *testing.T) {
	body := readRenderYAML(t)

	// Anchored on the phrase rather than the file's layout, so reindenting or reordering the comment
	// does not break it. The alternation matches both the digit and the spelled-out word, because
	// either is a natural way to write it and neither should require finding the other.
	re := regexp.MustCompile(`(?i)issuing ([a-z0-9-]+) database queries`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("render.yaml no longer says how many queries a dashboard render issues.\n" +
			"  The comment explaining the /health check promises a figure, and a reader who cannot find\n" +
			"  it has no way to judge the cost of probing \"/\". Either restore the sentence, or say why\n" +
			"  the number is no longer worth stating.")
	}

	documented, err := parseQueryCount(m[1])
	if err != nil {
		t.Fatalf("render.yaml documents an unreadable query count %q: %v", m[1], err)
	}

	// The same number, as a literal, in the measurement. Taken as a constant rather than shared with
	// that test: the two tests are in the same package, so this could be a variable, but then
	// changing the expected count would change both assertions at once and the guard would pass by
	// construction. Spelling it twice is what makes the disagreement detectable.
	const measured = 2
	if documented != measured {
		t.Errorf("render.yaml documents %d database queries for a dashboard render; the pool test "+
			"measures %d.\n"+
			"  One of the two is out of date. If the dashboard changed, update the comment in "+
			"render.yaml and the constant in TestDashboardLoadLeavesTheHealthProbeAConnection "+
			"together. If it did not, the comment is simply wrong and should be corrected to %d.",
			documented, measured, measured)
	}
}

// parseQueryCount reads a count written as a digit or as an English number, in either case.
func parseQueryCount(s string) (int, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return n, nil
	}
	words := map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	}
	if n, ok := words[strings.ToLower(s)]; ok {
		return n, nil
	}
	return 0, os.ErrInvalid
}

// readRenderYAML returns the contents of render.yaml.
//
// Resolved relative to this file rather than the working directory, for the reason given in
// readReadme: the tests run with tests/ as their working directory, and depending on a helper that
// chdirs to the repository root would make the result depend on test order.
func readRenderYAML(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine this file's path")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "render.yaml")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}
