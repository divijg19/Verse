// Package testsupport holds logic shared by the database-backed test packages.
//
// It deliberately does not import "testing". Each caller receives a reason string and decides
// whether to skip, so this package carries no test flags and the gate is unit-testable on its own.
package testsupport

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

const (
	// DSNEnv names the variable a test database is read from.
	//
	// It is intentionally not interchangeable with DATABASE_URL. That variable also boots the
	// application, and the local-development instructions tell you to export it, so honoring it
	// here would let a routine `go test ./...` truncate whatever the developer's shell happened
	// to be pointed at. A name that cannot be confused with a production variable is the first of
	// two gates.
	DSNEnv = "VERSE_E2E_DATABASE_URL"

	// ConsentEnv must be set to exactly "1" before any test may delete rows.
	//
	// The DSN alone is not consent: a stale export from an earlier session is enough to satisfy
	// the first gate, and the second gate is what forces a deliberate, per-invocation acknowledgement
	// that these tests run TRUNCATE against poems.
	ConsentEnv = "VERSE_E2E_ALLOW_DESTRUCTIVE"

	// disposableNameMarker is required to appear in the target database's name.
	//
	// This is the weaker of the two gates and is a backstop, not the protection. Its purpose is to
	// make an accidental target loud: a production database is not called verse_test, so a
	// misconfigured DSN fails here even if the consent variable is still exported in the shell.
	// The authoritative protection is DSNEnv, which no production configuration sets.
	disposableNameMarker = "test"
)

// DisposableDSN reports the DSN that database-backed tests may use, or explains why they must not
// run. A non-empty reason means the caller must skip; it never means fail.
//
// Skipping rather than failing is deliberate. These gates describe the developer's environment, not
// the correctness of the code, so a contributor who has never configured a test database should get
// a passing `go test ./...` and an explanation rather than a red build.
//
// Two independent gates must both open before a destructive statement may run:
//
//	VERSE_E2E_DATABASE_URL      the only accepted source of a test DSN
//	VERSE_E2E_ALLOW_DESTRUCTIVE set to exactly "1", acknowledging that poems is emptied
//
// Both are required because each covers the other's failure: the name check below catches a stale
// consent export, and the DSN requirement catches the far more common case of a developer who has
// DATABASE_URL exported to run the app and has never heard of the test variable.
func DisposableDSN() (dsn string, reason string) {
	dsn = strings.TrimSpace(os.Getenv(DSNEnv))
	if dsn == "" {
		return "", fmt.Sprintf(
			"set %s to run database-backed tests. %s is deliberately not accepted as a fallback: "+
				"it also boots the application, so accepting it would let a routine `go test ./...` "+
				"truncate whatever the shell is pointed at.",
			DSNEnv, "DATABASE_URL")
	}

	if got := strings.TrimSpace(os.Getenv(ConsentEnv)); got != "1" {
		return "", fmt.Sprintf(
			"set %s=1 to run database-backed tests. They TRUNCATE poems, so they are gated on an "+
				"explicit acknowledgement rather than on %s alone.",
			ConsentEnv, DSNEnv)
	}

	name, err := DatabaseName(dsn)
	if err != nil {
		return "", fmt.Sprintf(
			"cannot confirm the target of %s is a disposable database: %v. Point it at a "+
				"%s.../%s_test database.", DSNEnv, err, "postgres://host", "verse")
	}

	if !strings.Contains(strings.ToLower(name), disposableNameMarker) {
		return "", fmt.Sprintf(
			"refusing to run destructive tests against database %q: the name must contain %q so a "+
				"production target fails loudly. Use a separate %s database rather than sharing one "+
				"with the application.", name, disposableNameMarker, "verse_test")
	}

	return dsn, ""
}

// DatabaseName extracts the database name from a postgres URL DSN.
//
// Only URL form is accepted. pgx also accepts a keyword/value DSN such as
// "host=localhost dbname=verse", which cannot be reliably distinguished from a URL by inspection,
// so the gate refuses it rather than guessing. Failing closed is the correct behavior for a check
// whose purpose is to establish that a target is disposable.
func DatabaseName(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("not a valid URL: %w", err)
	}

	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", fmt.Errorf("expected a postgres:// URL, got scheme %q", u.Scheme)
	}

	// Trim both ends: a DSN written with a trailing slash (".../verse_test/") is legal but would
	// otherwise yield the name "verse_test/", which fails the marker check for the wrong reason and
	// reports a confusing name back to the developer.
	name := strings.Trim(u.Path, "/")
	if name == "" {
		return "", fmt.Errorf("no database name in the URL path")
	}

	return name, nil
}
