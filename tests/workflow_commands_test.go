package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This file exists because a renamed flag broke the build.
//
// v0.5.0 renamed cmd/publish's -dry-run to -list, and the CI workflow still called the old
// name. The build went red in a step that runs a command, so the failure was a flag-parsing error
// in a log rather than a test failure, and nothing local had said anything was wrong.
//
// It was the second time in one release: the CI step that type-checked the TypeScript named
// `bunx tsc --noEmit` after the dependency that provided it had been removed. Both were changes to
// a command's surface, and both were invisible until CI ran.
//
// So the surface is now checked where the change is made. The workflows and the documentation name
// commands and their flags; the commands define their flags. A name in one that the other does not
// have is a break, and it is found by the test suite rather than by the build.

// commandInvocation finds a `go run ./cmd/<name>` in a line of shell.
var commandInvocation = regexp.MustCompile(`go run \./cmd/([a-z0-9-]+)`)

// flagDefinition finds a flag's name in a command's source.
//
// The `flag` package takes the name as its first string argument, and the Var forms take the
// pointer first and the name second. Both shapes are covered by taking the first quoted string
// after the call.
var flagDefinition = regexp.MustCompile(`flag\.[A-Za-z0-9]+\(`)

// invocationFlags returns the flags a command is called with in one line of shell.
func invocationFlags(rest string) []string {
	// The command ends at the first shell metacharacter, so a redirect or a pipe does not turn
	// its file name into a flag.
	if at := strings.IndexAny(rest, "|&;"); at >= 0 {
		rest = rest[:at]
	}

	var flags []string
	for _, token := range strings.Fields(rest) {
		if !strings.HasPrefix(token, "-") || token == "-" || token == "--" {
			continue
		}

		name := strings.TrimLeft(token, "-")
		if at := strings.Index(name, "="); at >= 0 {
			name = name[:at]
		}
		if name != "" {
			flags = append(flags, name)
		}
	}
	return flags
}

// definedFlags returns the flags a command defines, by name.
func definedFlags(t *testing.T, command string) map[string]bool {
	t.Helper()

	dir := filepath.Join(repoRoot(t), "cmd", command)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	defined := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}

		for _, match := range flagDefinition.FindAllStringIndex(string(body), -1) {
			rest := string(body[match[1]:])
			start := strings.Index(rest, `"`)
			if start < 0 {
				continue
			}
			rest = rest[start+1:]
			end := strings.Index(rest, `"`)
			if end < 0 {
				continue
			}
			defined[rest[:end]] = true
		}
	}

	return defined
}

// checkInvocations asserts that every command invocation in a body names flags the command has.
func checkInvocations(t *testing.T, name, body string) {
	t.Helper()

	// A line ending in a backslash continues onto the next one, so the lines are joined before
	// the flags are read. A flag on the continuation would otherwise be missed.
	body = strings.ReplaceAll(body, "\\\n", " ")

	for _, line := range strings.Split(body, "\n") {
		match := commandInvocation.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		command := match[1]
		rest := line[strings.Index(line, match[0]):]

		defined := definedFlags(t, command)
		for _, flag := range invocationFlags(rest) {
			if !defined[flag] {
				t.Errorf("%s: %q is called with -%s, which %s does not define", name, command, flag, command)
			}
		}
	}
}

// TestTheWorkflowsCallTheCommandsWithFlagsTheyDefine is the guard on the workflows.
//
// A workflow is executed, so a flag it names that the command does not have stops the build. The
// build is where it should stop -- but it should not be the first place anyone finds out.
func TestTheWorkflowsCallTheCommandsWithFlagsTheyDefine(t *testing.T) {
	dir := filepath.Join(repoRoot(t), ".github", "workflows")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	found := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}

		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}

		found++
		checkInvocations(t, entry.Name(), string(body))
	}

	if found == 0 {
		t.Fatal("no workflow was found; the guard is not looking at anything")
	}
}

// TestTheDocumentationCallsTheCommandsWithFlagsTheyDefine is the guard on the documentation.
//
// RUNNING.md and CONTRIBUTING.md tell the operator and the contributor what to run, and a flag that
// has been renamed makes the instruction wrong in the same way it makes the workflow wrong. The
// documentation is read on its own, though: nobody runs it.
func TestTheDocumentationCallsTheCommandsWithFlagsTheyDefine(t *testing.T) {
	for _, name := range []string{"RUNNING.md", "CONTRIBUTING.md"} {
		body, err := os.ReadFile(filepath.Join(repoRoot(t), name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}

		checkInvocations(t, name, string(body))
	}
}
