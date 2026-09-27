package testsupport

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Helpers for tests that need a database region of their own.
//
// A test that creates or drops schema, or that needs a database in a known unmigrated state, cannot
// share the one the rest of the suite truncates. These helpers exist so that isolation is expressed
// once rather than re-derived in each test package, and so the two packages cannot drift apart on
// the details that make it work -- notably the case-folding trap in WithSearchPath.

// NormalizeSchemaName makes a test name usable as a schema identifier.
//
// Schema names are lowercased because they are used as an unqualified search_path value, and
// Postgres folds an unquoted identifier in that position to lower case. A schema created quoted as
// "t_MyTest" and then searched for as "t_mytest" does not exist, and the failure is the unhelpful
// "no schema has been selected to create in".
func NormalizeSchemaName(prefix, testName string) string {
	cleaned := strings.NewReplacer("/", "_", " ", "_", "-", "_", ".", "_").Replace(testName)
	return prefix + strings.ToLower(cleaned)
}

// WithSearchPath returns the DSN with search_path set to the given schema.
func WithSearchPath(dsn, schema string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}

	// An existing search_path is replaced rather than appended to, so a test always runs against
	// exactly the schema it asked for.
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// CreateSchema creates a schema, doing nothing if it already exists.
//
// Not safe to call concurrently for the same name from two connections, so callers that may race
// should create schemas in a single place rather than relying on this to be idempotent under
// contention.
func CreateSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	_, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+QuoteIdent(schema))
	if err != nil {
		return fmt.Errorf("create schema %s: %w", schema, err)
	}
	return nil
}

// DropSchema drops a schema and everything in it, if present.
func DropSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	_, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+QuoteIdent(schema)+` CASCADE`)
	if err != nil {
		return fmt.Errorf("drop schema %s: %w", schema, err)
	}
	return nil
}

// QuoteIdent quotes an identifier for interpolation into SQL.
//
// The only values interpolated are schema names these helpers construct from test names, so this is
// not defending against an input path; it is here so the interpolated values are visibly quoted
// rather than relying on every caller to remember.
func QuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// ConnectScratch returns a pool whose search_path is the named schema, and a cleanup that drops it.
//
// The schema is created on a separate connection, because the returned pool's search_path points at
// it and so cannot be used to create it.
func ConnectScratch(ctx context.Context, dsn, schema string) (*pgxpool.Pool, func() error, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("parse dsn: %w", err)
	}

	// Drop first: a previous run that failed before its cleanup would otherwise leave a schema whose
	// contents make this test's assertions meaningless.
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}
	if err := DropSchema(ctx, admin, schema); err != nil {
		admin.Close()
		return nil, nil, err
	}
	if err := CreateSchema(ctx, admin, schema); err != nil {
		admin.Close()
		return nil, nil, err
	}
	admin.Close()

	scoped, err := WithSearchPath(dsn, schema)
	if err != nil {
		return nil, nil, err
	}
	scopedCfg, err := pgxpool.ParseConfig(scoped)
	if err != nil {
		return nil, nil, fmt.Errorf("parse scoped dsn: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, scopedCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to schema %s: %w", schema, err)
	}

	// The cleanup error is returned rather than discarded. A schema that fails to be dropped is not
	// cosmetic: ConnectScratch drops before it creates, so a leaked schema would be silently reused
	// by a later run and could make this test's assertions meaningless. Reporting it lets the caller
	// fail rather than leave that behind.
	cleanup := func() error {
		pool.Close()

		detached := context.WithoutCancel(ctx)
		fresh, err := pgxpool.NewWithConfig(detached, cfg)
		if err != nil {
			return fmt.Errorf("reconnect to drop schema %s: %w", schema, err)
		}
		defer fresh.Close()

		return DropSchema(detached, fresh, schema)
	}

	return pool, cleanup, nil
}

// EnsurePackageSchema creates a schema for a test package if it is not already there, and returns a
// DSN scoped to it.
//
// This is the difference between ConnectScratch, which gives one test its own schema and drops it
// afterwards, and this, which gives a whole package one schema and leaves it. A package's tests run
// sequentially inside one process and share it deliberately; what must not be shared is state between
// packages, and that is what this prevents.
//
// Idempotent, so every test in the package can call it without coordinating. The name is normalised
// because it is used as an unqualified search_path value, which folds to lower case -- see
// NormalizeSchemaName.
func EnsurePackageSchema(ctx context.Context, dsn, schema string) (string, error) {
	if err := checkSchemaName(schema); err != nil {
		return "", err
	}

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return "", fmt.Errorf("connect to create schema %s: %w", schema, err)
	}
	defer admin.Close()

	if err := CreateSchema(ctx, admin, schema); err != nil {
		return "", err
	}

	return WithSearchPath(dsn, schema)
}

// checkSchemaName rejects a name that is not a plain lower-case identifier.
//
// The name is interpolated into DDL after quoting and is used as a search_path value, so anything
// with a quote, a space or upper-case in it is either a mistake or an injection. Rejecting it here
// means no caller has to think about it.
func checkSchemaName(schema string) error {
	if schema == "" {
		return errors.New("schema name is empty")
	}
	if schema != strings.ToLower(schema) {
		return fmt.Errorf("schema name %q must be lower case; it is used as a search_path value", schema)
	}
	// De Morgan applied deliberately: "not a lower-case letter AND not a digit AND not an
	// underscore" says exactly what is allowed, which is the complement that reads correctly.
	if strings.ContainsFunc(schema, func(r rune) bool {
		return r != '_' && !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789", r)
	}) {
		return fmt.Errorf("schema name %q must contain only lower-case letters, digits and underscores", schema)
	}
	return nil
}
