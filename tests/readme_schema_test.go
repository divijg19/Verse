package tests

import (
	"context"
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
