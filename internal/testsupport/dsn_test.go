package testsupport

import (
	"strings"
	"testing"
)

// TestDisposableDSNGates covers every way the gate can refuse, and the single way it opens.
//
// The gate is the only thing standing between a routine `go test ./...` and the contents of whatever
// database the developer's shell points at, so each closed path is asserted individually. A
// regression that reopened any one of them would not be caught by a test that only checks the happy
// path.
func TestDisposableDSNGates(t *testing.T) {
	const goodDSN = "postgres://verse:verse@localhost:5432/verse_test?sslmode=disable"

	cases := []struct {
		name string
		dsn  string
		// consent is the raw value for ConsentEnv; unset when nil.
		consent *string
		// wantOpen is whether the gate should admit the DSN.
		wantOpen bool
		// wantReason must appear in the explanation when the gate is closed.
		wantReason string
	}{
		{
			name:       "both gates open",
			dsn:        goodDSN,
			consent:    ptr("1"),
			wantOpen:   true,
			wantReason: "",
		},
		{
			name:       "no DSN at all",
			dsn:        "",
			consent:    ptr("1"),
			wantOpen:   false,
			wantReason: DSNEnv,
		},
		{
			name:       "DSN present but no consent",
			dsn:        goodDSN,
			consent:    nil,
			wantOpen:   false,
			wantReason: ConsentEnv,
		},
		{
			name:       "consent set to something other than 1",
			dsn:        goodDSN,
			consent:    ptr("true"),
			wantOpen:   false,
			wantReason: ConsentEnv,
		},
		{
			name:       "consent true is not 1, and 1 is not true",
			dsn:        goodDSN,
			consent:    ptr("yes"),
			wantOpen:   false,
			wantReason: ConsentEnv,
		},
		{
			name:       "consent with surrounding whitespace is not 1",
			dsn:        goodDSN,
			consent:    ptr(" 1 "),
			wantOpen:   true,
			wantReason: "",
		},
		{
			name:       "production-shaped database name is refused",
			dsn:        "postgres://verse:verse@db.example.supabase.co:5432/verse?sslmode=require",
			consent:    ptr("1"),
			wantOpen:   false,
			wantReason: `must contain "test"`,
		},
		{
			name:       "a name merely containing test as a substring is accepted",
			dsn:        "postgres://verse:verse@localhost:5432/latest?sslmode=disable",
			consent:    ptr("1"),
			wantOpen:   true,
			wantReason: "",
		},
		{
			name:       "name check is case insensitive",
			dsn:        "postgres://verse:verse@localhost:5432/VERSE_TEST?sslmode=disable",
			consent:    ptr("1"),
			wantOpen:   true,
			wantReason: "",
		},
		{
			name:       "keyword/value DSN is refused rather than guessed at",
			dsn:        "host=localhost dbname=verse_test user=verse",
			consent:    ptr("1"),
			wantOpen:   false,
			wantReason: "postgres://",
		},
		{
			name:       "DSN with no database name is refused",
			dsn:        "postgres://verse:verse@localhost:5432/",
			consent:    ptr("1"),
			wantOpen:   false,
			wantReason: "no database name",
		},
		{
			name:       "unparseable DSN is refused",
			dsn:        "postgres://verse:verse@local host:5432/verse_test",
			consent:    ptr("1"),
			wantOpen:   false,
			wantReason: "cannot confirm",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(DSNEnv, tc.dsn)
			if tc.consent != nil {
				t.Setenv(ConsentEnv, *tc.consent)
			} else {
				t.Setenv(ConsentEnv, "")
			}

			dsn, reason := DisposableDSN()

			if tc.wantOpen {
				if reason != "" {
					t.Fatalf("gate refused a valid configuration: %s", reason)
				}
				if dsn != tc.dsn {
					t.Fatalf("returned DSN = %q, want %q", dsn, tc.dsn)
				}
				return
			}

			if reason == "" {
				t.Fatal("gate admitted a configuration it should refuse")
			}
			if dsn != "" {
				t.Fatalf("returned a DSN alongside a refusal: %q", dsn)
			}
			if !strings.Contains(reason, tc.wantReason) {
				t.Fatalf("refusal does not explain itself.\n got: %s\nwant substring: %s", reason, tc.wantReason)
			}
		})
	}
}

// TestDisposableDSNIgnoresDatabaseURL is the regression this gate exists for.
//
// DATABASE_URL is the variable the local-development instructions tell you to export, and it is the
// one the application itself boots from. If the gate ever accepted it, a developer following the
// documented setup and then running `go test ./...` would empty their real database.
func TestDisposableDSNIgnoresDatabaseURL(t *testing.T) {
	const productionDSN = "postgres://verse:realpassword@db.example.supabase.co:5432/verse?sslmode=require"

	t.Setenv(DSNEnv, "")
	t.Setenv(ConsentEnv, "1")
	t.Setenv("DATABASE_URL", productionDSN)

	dsn, reason := DisposableDSN()
	if reason == "" {
		t.Fatal("gate opened with only DATABASE_URL set")
	}
	if dsn != "" {
		t.Fatalf("gate returned a DSN: %q", dsn)
	}
	if !strings.Contains(reason, "DATABASE_URL") {
		t.Fatalf("refusal should name the variable it deliberately ignored: %s", reason)
	}
}

// TestDatabaseName covers the parsing the gate depends on, including the cases it must refuse.
func TestDatabaseName(t *testing.T) {
	cases := []struct {
		dsn   string
		want  string
		isErr bool
	}{
		{dsn: "postgres://u:p@localhost:5432/verse_test?sslmode=disable", want: "verse_test"},
		{dsn: "postgresql://u:p@localhost:5432/verse_test", want: "verse_test"},
		{dsn: "postgres://localhost/verse_test", want: "verse_test"},
		{dsn: "postgres://u:p@localhost:5432/verse_test/", want: "verse_test"},
		{dsn: "postgres://u:p@localhost:5432/", isErr: true},
		{dsn: "postgres://u:p@localhost:5432", isErr: true},
		{dsn: "mysql://u:p@localhost:3306/verse_test", isErr: true},
		{dsn: "host=localhost dbname=verse_test", isErr: true},
		{dsn: "", isErr: true},
		{dsn: "://nope", isErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.dsn, func(t *testing.T) {
			got, err := DatabaseName(tc.dsn)
			if tc.isErr {
				if err == nil {
					t.Fatalf("expected an error, got name %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("name = %q, want %q", got, tc.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }
