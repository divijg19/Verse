package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/migrate"
	appserver "github.com/divijg19/Verse/internal/server"
	"github.com/divijg19/Verse/internal/testsupport"
)

// TestRequireSchemaRefusesUntilMigrated locks the contract between the migration runner and server
// startup.
//
// The application no longer creates its own schema, so a deploy that arrives before its migrations
// have run would otherwise start up and then fail on the first query with a missing-relation error
// from deep inside a handler. RequireSchema turns that into a refusal at boot.
//
// Both halves are asserted. The refusal is the new behavior and the part that could silently
// regress; the acceptance is what stops the check from being written so strictly that it would also
// reject a correctly migrated database.
//
// This runs against its own schema, because it needs a region with no poems table at all, which is
// exactly the state the shared test schema is never in.
func TestRequireSchemaRefusesUntilMigrated(t *testing.T) {
	dsn, reason := testsupport.DisposableDSN()
	if reason != "" {
		t.Skip(reason)
	}

	ctx := context.Background()
	schema := testsupport.NormalizeSchemaName("require_t_", t.Name())

	// The schema has to exist before the scoped connection can use it, and the scoped connection is
	// the one searching for it, so creation happens on an unscoped connection first.
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect for schema setup: %v", err)
	}
	if err := testsupport.DropSchema(ctx, admin, schema); err != nil {
		admin.Close()
		t.Fatalf("drop stale schema: %v", err)
	}
	if err := testsupport.CreateSchema(ctx, admin, schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	admin.Close()
	t.Cleanup(func() {
		fresh, err := pgxpool.New(context.WithoutCancel(ctx), dsn)
		if err != nil {
			return
		}
		defer fresh.Close()
		_ = testsupport.DropSchema(context.WithoutCancel(ctx), fresh, schema)
	})

	// RequireSchema reads the global pool, so the global pool is what has to be pointed at the
	// scratch schema. Both assertions below are made against that connection.
	scoped, err := testsupport.WithSearchPath(dsn, schema)
	if err != nil {
		t.Fatalf("scoped dsn: %v", err)
	}
	t.Setenv("DATABASE_URL", scoped)

	closePool(t)
	t.Cleanup(func() { closePool(t) })
	if err := database.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Before migrations: refuse, and say what to do about it.
	if err := database.RequireSchema(ctx); err == nil {
		t.Fatal("RequireSchema accepted a schema that had never been migrated")
	} else {
		msg := err.Error()
		if !strings.Contains(msg, "go run ./cmd/migrate") {
			t.Fatalf("refusal does not tell the operator how to fix it: %q", msg)
		}
		if !strings.Contains(msg, "poems") {
			t.Fatalf("refusal does not name the missing table: %q", msg)
		}
	}

	// After migrations: accept.
	if _, err := migrate.Run(ctx, database.Pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := database.RequireSchema(ctx); err != nil {
		t.Fatalf("RequireSchema rejected a migrated schema: %v", err)
	}
}

// closePool closes and clears the global pool, so a test can reconnect it against a different DSN.
func closePool(t *testing.T) {
	t.Helper()
	if database.Pool != nil {
		database.Pool.Close()
		database.Pool = nil
	}
}

// TestPassphraseLengthFloorIsEnforced pins the minimum, from both sides.
//
// The floor did not exist before v0.4.2, which made the passphrase guarding the entire archive weaker
// than the HMAC key next to it: the secret needed 32 characters, the passphrase needed one. The
// boundary is asserted exactly so a future change to the comparison cannot drift unnoticed.
func TestPassphraseLengthFloorIsEnforced(t *testing.T) {
	cases := []struct {
		name       string
		passphrase string
		wantErr    bool
	}{
		{"empty", "", true},
		{"one character", "a", true},
		{"one below the minimum", strings.Repeat("a", 15), true},
		{"exactly the minimum", strings.Repeat("a", 16), false},
		{"above the minimum", strings.Repeat("a", 32), false},
		{"whitespace only", strings.Repeat(" ", 40), true},
		{"mostly whitespace", strings.Repeat(" ", 20) + "a", true},
		{"a real passphrase", "correct horse battery staple", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VERSE_AUTHORIZATION", tc.passphrase)
			t.Setenv("VERSE_AUTH_SECRET", "0123456789abcdef0123456789abcdef")

			_, err := appserver.NewRouter()
			if tc.wantErr && err == nil {
				t.Fatalf("a %d-character passphrase was accepted, minimum is 16", len(tc.passphrase))
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("a %d-character passphrase was rejected: %v", len(tc.passphrase), err)
			}
			// The message must name the variable and both minimums, since an operator reading a
			// crash-looping deploy needs all three facts at once.
			if tc.wantErr {
				if !strings.Contains(err.Error(), "VERSE_AUTHORIZATION") {
					t.Errorf("the error does not name the variable: %v", err)
				}
				if !strings.Contains(err.Error(), "at least 16") {
					t.Errorf("the error does not state the passphrase minimum: %v", err)
				}
			}
		})
	}
}

// TestSecretLengthFloorIsStillEnforced guards the boundary that already existed, so adding the
// passphrase floor did not disturb it.
func TestSecretLengthFloorIsStillEnforced(t *testing.T) {
	cases := []struct {
		secret  string
		wantErr bool
	}{
		{secret: strings.Repeat("s", 31), wantErr: true},
		{secret: strings.Repeat("s", 32), wantErr: false},
	}

	for _, tc := range cases {
		t.Setenv("VERSE_AUTHORIZATION", "a-sufficiently-long-passphrase")
		t.Setenv("VERSE_AUTH_SECRET", tc.secret)

		_, err := appserver.NewRouter()
		if tc.wantErr && err == nil {
			t.Errorf("a %d-character secret was accepted", len(tc.secret))
		}
		if !tc.wantErr && err != nil {
			t.Errorf("a %d-character secret was rejected: %v", len(tc.secret), err)
		}
	}
}
