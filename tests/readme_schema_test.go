package tests

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/database"
)

// This file exists because the README documented a schema that has never existed.
//
// It described a users table, and poems.user_id, poems.mood and poems.prompt_used. None of them were
// ever created. The section sat in the front-door file, labeled "MVP", and was wrong for as long as
// the project has existed -- which is the whole argument for checking it rather than correcting it
// once: a correction decays back into a lie at exactly the rate the schema changes, and the schema
// changes whenever a feature lands.
//
// The check is against the live schema rather than against the SQL files, so it catches drift in both
// directions. A column added by a migration and not documented fails, and a column documented but
// never created fails -- which is precisely the bug this replaces.

// documentedSchemaBlock matches the fenced block the README's schema section is required to contain.
//
// A double-quoted string rather than a raw one: the pattern contains triple backticks, which would
// terminate a raw string literal.
var documentedSchemaBlock = regexp.MustCompile(
	"(?s)<!-- documented-schema:begin -->\\s*```sql\\n(.*?)\\n```\\s*<!-- documented-schema:end -->")

// tableDecl matches one `table name {` ... `}` declaration.
var tableDecl = regexp.MustCompile(`(?s)table (\w+) \{(.*?)\n\}`)

// columnDecl matches one `name type` line inside a table, ignoring comments.
var columnDecl = regexp.MustCompile(`(?m)^\s*(\w+)\s+(uuid|text|timestamp|timestamptz|bigint|integer|bytea)\b`)

// typeAliases maps the type names Postgres reports onto the aliases the README documents, so the
// comparison is on meaning rather than on spelling.
var typeAliases = map[string]string{
	"uuid":                        "uuid",
	"text":                        "text",
	"character varying":           "text",
	"timestamp without time zone": "timestamp",
	"timestamp with time zone":    "timestamptz",
	"bigint":                      "bigint",
	"integer":                     "integer",
	"bytea":                       "bytea",
}

func TestReadmeSchemaMatchesTheDatabase(t *testing.T) {
	connectTestDB(t)

	documented := documentedSchema(t)
	actual := liveSchema(t)

	if len(documented) == 0 {
		t.Fatal("no schema was parsed out of the README; the documented-schema markers are missing " +
			"or the block format changed")
	}

	// Tables present in one and not the other, named explicitly, because "a mismatch" is not
	// actionable and a list of names is.
	for _, table := range missingKeys(documented, actual) {
		t.Errorf("the README documents table %q, which does not exist in the schema", table)
	}
	for _, table := range missingKeys(actual, documented) {
		t.Errorf("table %q exists in the schema but is not documented in the README", table)
	}

	// Columns, for the tables both agree exist.
	for table, docColumns := range documented {
		liveColumns, ok := actual[table]
		if !ok {
			continue // already reported above
		}
		for _, column := range missingKeys(docColumns, liveColumns) {
			t.Errorf("the README documents %s.%s, which does not exist in the schema", table, column)
		}
		for _, column := range missingKeys(liveColumns, docColumns) {
			t.Errorf("the schema has %s.%s, which the README does not document", table, column)
		}

		// Types, compared by meaning. A column that exists with the wrong type is a subtler and
		// worse bug than one that is merely undocumented.
		for column, wantType := range docColumns {
			gotType, ok := liveColumns[column]
			if !ok {
				continue // already reported above
			}
			if gotType != wantType {
				t.Errorf("%s.%s is %s in the schema but documented as %s",
					table, column, gotType, wantType)
			}
		}
	}
}

// TestReadmeDeclaresNoPhantomSchema pins the specific claims that were wrong for the life of the
// project, so a well-meaning edit that reintroduces one is caught with a pointed message rather than
// as a generic mismatch.
//
// Scoped to the declared identifiers rather than the whole document. The prose legitimately names
// these things -- to say they do not exist -- and a substring search over the entire README flags the
// correction as the bug. What matters is that none of them is *declared*.
func TestReadmeDeclaresNoPhantomSchema(t *testing.T) {
	schema := documentedSchema(t)
	if len(schema) == 0 {
		t.Fatal("no schema was parsed out of the README; the documented-schema markers are missing " +
			"or the block format changed")
	}

	for _, phantom := range []string{"users", "user_id", "prompt_used", "mood"} {
		if columns, ok := schema[phantom]; ok {
			t.Errorf("the README declares table %q, which does not exist; "+
				"there is no user table, no registration and no multi-tenancy", phantom)
			_ = columns
		}
		for table, columns := range schema {
			if _, ok := columns[phantom]; ok {
				t.Errorf("the README declares column %s.%s, which does not exist", table, phantom)
			}
		}
	}

	// And positively: the README has to say why. A schema section that simply omits users leaves a
	// reader wondering whether it is an oversight.
	if !strings.Contains(readReadme(t), "no `users` table") {
		t.Error("the README does not state that there is no users table; " +
			"its absence should be deliberate and visible, not a silent omission")
	}
}

// documentedSchema parses the README's schema block into table -> column -> type.
func documentedSchema(t *testing.T) map[string]map[string]string {
	t.Helper()

	match := documentedSchemaBlock.FindStringSubmatch(readReadme(t))
	if match == nil {
		return nil
	}

	out := map[string]map[string]string{}
	for _, table := range tableDecl.FindAllStringSubmatch(match[1], -1) {
		columns := map[string]string{}
		for _, column := range columnDecl.FindAllStringSubmatch(table[2], -1) {
			columns[column[1]] = column[2]
		}
		out[table[1]] = columns
	}
	return out
}

// liveSchema reads table -> column -> normalized type from the connected database.
func liveSchema(t *testing.T) map[string]map[string]string {
	t.Helper()

	rows, err := database.Pool.Query(context.Background(), `
        SELECT table_name, column_name, data_type
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name NOT LIKE 'pg_%'
        ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatalf("read the live schema: %v", err)
	}
	defer rows.Close()

	out := map[string]map[string]string{}
	for rows.Next() {
		var table, column, dataType string
		if err := rows.Scan(&table, &column, &dataType); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		normalized, ok := typeAliases[dataType]
		if !ok {
			t.Errorf("column %s.%s has type %q, which the README's type vocabulary does not "+
				"cover; add an alias in typeAliases so the check stays meaningful", table, column, dataType)
			continue
		}
		if out[table] == nil {
			out[table] = map[string]string{}
		}
		out[table][column] = normalized
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}
	return out
}

// missingKeys returns the keys of want that are absent from got, sorted for a stable message.
//
// Generic so the same comparison serves both levels of the check: table names, and the column names
// within a table.
func missingKeys[V any](want, got map[string]V) []string {
	var out []string
	for key := range want {
		if _, ok := got[key]; !ok {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// readReadme returns the contents of README.md.
//
// Resolved relative to this file rather than the working directory, because the tests run with tests/
// as their working directory and the helpers that chdir to the repository root are incidental to what
// they do. Depending on another test's side effect would make this one fail depending on test order.
func readReadme(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine this file's path")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "README.md")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// TestReadmeProjectTreeNamesOnlyRealPaths closes the second half of the documentation problem.
//
// The block above checks the schema against the live database. It cannot check prose, and the prose
// around it described a stack this repository has never contained: Dart, Jaspr, Next.js, sqlc, Goose
// and Atlas, a `poems.mood` enum, and a project tree naming six paths that do not exist. The failure
// mode is specific -- documentation that is confidently wrong, in a file whose *other* section is
// machine-checked, which is what makes it read as verified.
//
// The tree is parsed rather than matched, and every directory and file it names must exist. That is
// deliberately one-directional: a real path missing from the tree is incompleteness, not a falsehood,
// and failing on it would make this a chore to maintain. A *named* path that does not exist is the
// thing that misleads.
func TestReadmeProjectTreeNamesOnlyRealPaths(t *testing.T) {
	readme := readReadme(t)

	start := strings.Index(readme, "verse/\n")
	if start == -1 {
		t.Fatal("the README's project structure block no longer contains a verse/ root; " +
			"this test cannot tell whether the paths it names are real")
	}
	fence := strings.Index(readme[start:], "```")
	if fence == -1 {
		t.Fatal("the project structure block is not closed")
	}
	tree := readme[start : start+fence]

	// What this asserts, precisely, because an earlier version of it claimed more than it did.
	//
	// A hand-drawn tree is hierarchical: it writes "cmd/" on one line and an indented "server/" on the
	// next, so the string "cmd/server" never appears in it. A first attempt at this test searched the
	// tree for each full path and matched only the four flat entries at the bottom -- migrations, the
	// Dockerfile, go.mod, docs/RUNNING.md -- and passed. It was checking a fifth of what it appeared
	// to, which is the failure mode this file exists to prevent, arrived at from the other direction.
	//
	// So the claim is stated as what it is: every path below must exist, and its leaf name must appear
	// in the tree. Reconstructing the tree's paths by counting indentation is possible and was not
	// worth it -- a malformed line would then fail the test for a reason unrelated to whether the
	// documentation is truthful.
	//
	// One-directional on purpose. A named path that does not exist is a falsehood and fails. A real
	// path missing from the tree is incompleteness, and failing on that would make this a chore.
	paths := []string{
		"cmd/server", "cmd/migrate",
		"internal/server", "internal/handlers", "internal/services",
		"internal/database", "internal/migrate", "internal/models",
		"internal/presenters", "internal/export", "internal/clock", "internal/testsupport",
		"templ/layout.templ", "templ/editor.templ", "templ/heatmap.templ", "templ/security.go",
		"static/css/input.css", "static/js",
		"migrations", "docs/RUNNING.md", "Dockerfile", "go.mod",
	}

	// A path must be *populated*, not merely present, and that distinction is the whole reason this
	// test caught something in CI that it passed on locally.
	//
	// internal/middleware was an empty, untracked directory sitting in a working tree. os.Stat found
	// it, so a presence check passed, and the README tree named it as if the project contained it. A
	// fresh checkout has no such directory, so the same test failed in CI and passed at the machine
	// that wrote the README. The risk register had already recorded the path as documented-but-absent;
	// the local existence check is what overrode that.
	//
	// So a directory counts as present only if it contains at least one file, and an empty one is
	// reported by name. A file counts only if it is a regular file.
	//
	// The suite runs from tests/, and some tests chdir to the repository root, so the fallback the
	// other file-reading helpers use applies here too.
	exists := func(path string) bool {
		for _, candidate := range []string{path, filepath.Join("..", path)} {
			info, err := os.Stat(candidate)
			if err != nil {
				continue
			}
			if !info.IsDir() {
				return true
			}
			entries, readErr := os.ReadDir(candidate)
			if readErr != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					return true
				}
			}
			// An empty directory, or one holding only empty directories, is not part of the
			// project. Report it rather than accepting it.
			t.Errorf("the project tree names %q, which exists here only as an empty untracked "+
				"directory; a fresh checkout does not have it, so the documentation is wrong in "+
				"every clone but this one", path)
			return false
		}
		return false
	}

	var absent, unnamed int
	for _, path := range paths {
		if !exists(path) {
			absent++
			t.Errorf("the README's project tree names %q, which does not exist", path)
		}
		leaf := path[strings.LastIndex(path, "/")+1:]
		if !strings.Contains(tree, leaf) {
			unnamed++
			t.Errorf("the project tree does not mention %q, which this test asserts is real; "+
				"either the tree is out of date or this list is", leaf)
		}
	}

	// And the other direction within the tree itself: any filename it mentions must be real.
	//
	// The list above is the paths this test asserts, so a phantom file written into the tree's comment
	// column -- "dashboard.go, library.go, prompts.go, mood.go" -- would sail past it. Sweeping the
	// tree for filenames with a known extension catches that, and it is the failure this repository
	// actually had: the old tree named streak.go, mood.go, calendar.templ and three files under a
	// templ/components/ directory, none of which exist.
	fileInTree := regexp.MustCompile(`[A-Za-z0-9_.-]+\.(go|templ|sql|css|js|md|mod|yaml|yml|json)`)
	seen := map[string]struct{}{}
	for _, m := range fileInTree.FindAllString(tree, -1) {
		// go.mod and README.md are spelled at the tree root and are covered above; nothing to add by
		// re-checking them, and duplicating the assertion would only add noise to a failure.
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}

		if _, present := repositoryFilenames()[m]; !present {
			t.Errorf("the project tree names %q, which does not exist anywhere in the repository", m)
		}
	}

	if absent+unnamed == 0 {
		t.Logf("all %d path(s) asserted here exist, each leaf appears in the project tree, and "+
			"every filename it mentions is real (%d checked)", len(paths), len(seen))
	}
}

// TestReadmeClaimsNoTechnologyThatIsNotUsed is the other half, and it is the one that catches the
// style of falsehood rather than the accidental kind.
//
// A technology named in the stack list must appear in go.mod, package.json, or a workflow. That is a
// coarse test and deliberately so: it cannot tell whether Next.js is used *well*, and it does not
// try. It catches the failure this repository actually had, which was a stack list describing a
// different project while the build files beside it described this one.
func TestReadmeClaimsNoTechnologyThatIsNotUsed(t *testing.T) {
	readme := readReadme(t)

	// Technologies that appeared in the stack list and have never been in the build. Each is named
	// explicitly rather than derived, because the derivation is the interesting part and it is
	// straightforward: a tool is used if the build files mention it.
	absent := []struct{ name, why string }{
		{"Jaspr", "no Dart source, no pubspec, and no reference in any build file"},
		{"Next.js", "no JS project exists; the only JavaScript is navigation.js, editor.js and vendored htmx"},
		{"sqlc", "all queries are hand-written SQL in internal/services"},
		{"Goose", "the runner is internal/migrate, a bespoke implementation"},
		{"Atlas", "the runner is internal/migrate, a bespoke implementation"},
	}

	// The stack list is the part that makes a claim. Prose elsewhere may name a technology in order to
	// say it is not used, which is the opposite of a claim, so only the list is scanned.
	stackStart := strings.Index(readme, "Primary stack:")
	if stackStart == -1 {
		t.Fatal("the README no longer has a \"Primary stack:\" list; this test cannot check its claims")
	}
	stackEnd := strings.Index(readme[stackStart:], "---")
	if stackEnd == -1 {
		t.Fatal("the stack list is not terminated")
	}
	stack := readme[stackStart : stackStart+stackEnd]

	for _, tech := range absent {
		if strings.Contains(stack, tech.name) {
			t.Errorf("the README's stack list names %s, which this project does not use: %s",
				tech.name, tech.why)
		}
	}
}

// repositoryFilenames returns the set of basenames present in the repository, computed once.
//
// filepath.Glob does not support **, so the set is built by walking rather than by pattern. Directories
// that hold no project source are skipped: .git, node_modules, and the distroless build context would
// each add thousands of names and slow the test for no benefit.
var repositoryFilenamesOnce func() map[string]struct{}

func repositoryFilenames() map[string]struct{} {
	if repositoryFilenamesOnce != nil {
		return repositoryFilenamesOnce()
	}

	skip := map[string]bool{
		".git": true, "node_modules": true, "static/wasm": true,
		".claude": true, ".opencode": true,
	}

	names := map[string]struct{}{}
	root := "."
	if _, err := os.Stat("go.mod"); err != nil {
		root = ".."
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory that cannot be read is not this test's business; the paths it might have
			// contained are covered by the explicit list above.
			return nil //nolint:nilerr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil && skip[filepath.ToSlash(rel)] {
			return fs.SkipDir
		}
		if d != nil && !d.IsDir() {
			names[d.Name()] = struct{}{}
		}
		return nil
	})

	repositoryFilenamesOnce = func() map[string]struct{} { return names }
	return names
}
