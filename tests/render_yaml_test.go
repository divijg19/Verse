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

// TestRenderYamlBuildCommandStampsTheVersion holds the production build to cmd/server's real variable
// name.
//
// v0.4.11 added `var version` to cmd/server so an operator could tell which commit was running, and
// stamped it in the Dockerfile. The code comment in main.go then asserted that "the Dockerfile and
// render.yaml both pass -X main.version". render.yaml did not. The production build has therefore
// logged "version dev" on every boot since, and nothing noticed: no test read `buildCommand` at all,
// and the CI Container job tests the Dockerfile, not the blueprint Render actually uses.
//
// So this reads the blueprint and checks the flag. The variable name is not hardcoded in the
// expectation -- it is read from the Go source -- because hardcoding it is how the two drifted in the
// first place: a rename would leave a test asserting a name that no longer exists, and the build would
// quietly stop stamping.
func TestRenderYamlBuildCommandStampsTheVersion(t *testing.T) {
	body := readRenderYAML(t)

	// The build command is a YAML block scalar spanning several lines, so the substring that matters
	// is the `go build` line, not the whole script.
	var buildLine string
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "go build") && strings.Contains(line, "cmd/server") {
			buildLine = line
			break
		}
	}
	if buildLine == "" {
		t.Fatal("render.yaml's buildCommand no longer contains a `go build` of ./cmd/server.\n" +
			"  The service is built here, so a build that does not appear in this file is a service\n" +
			"  that cannot be built the way it is deployed.")
	}

	// Read the symbol from the source rather than assuming it.
	source, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "server", "main.go"))
	if err != nil {
		t.Fatalf("read cmd/server/main.go: %v", err)
	}
	varName := "version"
	if !regexp.MustCompile(`(?m)^var ` + regexp.QuoteMeta(varName) + ` = `).Match(source) {
		t.Fatalf("cmd/server/main.go no longer declares `var %s`.\n"+
			"  The -X flag stamps a package-level variable by name. If that variable was renamed or\n"+
			"  removed, the ldflag in render.yaml is stamping nothing and the boot log will say so\n"+
			"  without anyone reading it. Update both together, or drop the flag from both.", varName)
	}

	if !strings.Contains(buildLine, "-X main."+varName+"=") {
		t.Errorf("render.yaml builds cmd/server without stamping the version:\n  %s\n"+
			"  Expected a -X main.%s= flag. The deployed binary will report its version as \"dev\",\n"+
			"  which is indistinguishable from a local build in the one log an operator reads first.",
			strings.TrimSpace(buildLine), varName)
	}
}

// repoRoot resolves the repository root from a test file's own location, the same way
// readRenderYAML does.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine this file's path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..")
}

// TestDocumentedRoleSetupMatchesCI holds db/roles.sql to being the one privilege model.
//
// db/roles.sql and the Least privilege job were two independent copies of the same grant set: the
// operator ran the file, CI ran its own inline SQL, and the two were free to diverge. They did. The
// file required a psql variable the documented command never passed, so the operator step failed on
// its first statement -- while CI stayed green for the whole of v0.4.11, because CI was never running
// the file it was supposed to be checking.
//
// CI now executes db/roles.sql directly, so the copies are gone. What this test protects is the
// remaining seam: the variable name. If RUNNING.md and ci.yml pass differently-named variables, the
// documented procedure fails exactly as it did before, and nothing else would notice -- because the
// only automated check applies the file with CI's own spelling.
func TestDocumentedRoleSetupMatchesCI(t *testing.T) {
	roles, err := os.ReadFile(filepath.Join(repoRoot(t), "db", "roles.sql"))
	if err != nil {
		t.Fatalf("read db/roles.sql: %v", err)
	}
	ciBytes, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	docs, err := os.ReadFile(filepath.Join(repoRoot(t), "RUNNING.md"))
	if err != nil {
		t.Fatalf("read RUNNING.md: %v", err)
	}

	const variable = "app_password"

	if !strings.Contains(string(roles), variable) {
		t.Fatalf("db/roles.sql no longer references %q, so the documented -v argument is wrong.", variable)
	}
	if !strings.Contains(string(ciBytes), "-v "+variable+"=") {
		t.Errorf("the Least privilege job does not pass -v %s= to db/roles.sql.\n"+
			"  The job runs the file directly, so a mismatch here means CI cannot execute it at all.",
			variable)
	}
	if !strings.Contains(string(docs), "-v "+variable+"=") {
		t.Errorf("RUNNING.md does not document -v %s= in the db/roles.sql command.\n"+
			"  The documented operator step is the one that failed before, because the file needed a\n"+
			"  variable the documentation never mentioned. It is the seam CI cannot check for you.", variable)
	}

	// The reverse direction: CI must run the file, not a copy of it. A reintroduced inline grant
	// block would be invisible to the three checks above.
	ci := string(ciBytes)
	ciStep := ci[strings.Index(ci, "Create the roles and grants"):]
	if !strings.Contains(ciStep, "db/roles.sql") {
		t.Error("the Least privilege job does not appear to run db/roles.sql.\n" +
			"  If it has gone back to an inline copy of the grants, the operator's file is untested again.")
	}
	if strings.Contains(ciStep, "GRANT SELECT, INSERT, UPDATE ON poems") {
		t.Error("the Least privilege job contains its own copy of the poems grant.\n" +
			"  Two copies of the privilege model is the condition this test exists to prevent.")
	}
}
