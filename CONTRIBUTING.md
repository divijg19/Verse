# Contributing

## Before you start

This is a private, single-author application, so the constraints that matter are narrow ones: the
work must not be lost, it must not leak, and the deployment has to keep working on the platform's
free tier.

Read `RUNNING.md` before changing anything operational. It is the reference for the deployment,
the environment variables, and the constraints that are not obvious from the code.

## Running the tests

The database-backed tests `TRUNCATE` tables, so they are gated on three things and **skip** without
them — deliberately, so `go test ./...` still passes for someone with no test database.

```bash
export VERSE_E2E_DATABASE_URL='postgres://…/verse_test?sslmode=disable'
export VERSE_E2E_ALLOW_DESTRUCTIVE=1

go test ./... -count=1 -race
```

The database name must contain `test`. That is the third gate: a typo pointing the suite at a
production database should fail loudly, not truncate an author's work.

The full command CI runs is `go test ./... -count=1 -race -v`, with no `-p 1`. Each test package has
its own schema, so serialising them is unnecessary; that was v0.4.3's change and it took the suite
from minutes to seconds.

## Generated files

`templ` artifacts are committed so `go build` works without a templ toolchain. That makes stale
generated code the failure mode: edit a `.templ` file, run a test that reads the stale `_templ.go`,
and ship the old markup with a passing build.

```bash
templ generate        # then commit the result
```

CI regenerates and fails if the result differs, so this cannot be forgotten. The version is pinned to
`v0.3.1020`; match it, or the diff will be unreadable.

## Style

- **Comments explain why, not what.** The codebase is dense with reasoning about *why* a thing is
  the way it is — that is the part worth maintaining. Do not add comments that restate the code.
- **Correctness before features.** A test that proves a property is worth more than a feature that
  could break one. When a change is subtle, write the test that would fail if it were wrong, and
  confirm it fails first.
  - **The three gates below must all be clean.** CI runs all three, and this guide used to name only
    the first — which is the exact substitution CI warns about in a comment of its own: `golangci-lint
    run` does not enforce the formatters section, so the formatting gate is load-bearing and must not
    be dropped in favor of relying on the linter.

    ```
    golangci-lint fmt          # formatting; the linter's run does not check this
    golangci-lint run ./...    # lint, including misspell and gosec
    staticcheck ./...          # whole-program analysis
    ```

    `staticcheck` is a separate gate deliberately. `golangci-lint`'s staticcheck is per-package; the
    standalone binary also catches unused identifiers across package boundaries, which is how two
    genuinely dead symbols were found here.

    CI additionally runs `govulncheck`, which needs no local counterpart to keep the tree mergeable.

    Where a `gosec` finding is a false positive, suppress it narrowly with `#nosec` and a comment
    explaining what the actual mitigation is — the pattern is already used throughout.
  - **Spellings are US.** `behavior`, `defense`, `recognize`. The linter enforces it.
  - **Three test suites govern files you would not expect them to.** `render.yaml` is read by
    `TestRenderYamlDashboardQueryCountMatchesTheMeasurement`; `README.md` is read by the `TestReadme*`
    suite; and `internal/handlers` is parsed by `TestEveryInternalErrorResponseIsLogged`, which fails
    if any handler writes a 500 anywhere except `fail500`. Editing any of the three is a change with a
    test suite attached, and the failure names the file. That last one is why adding an error response
    is now one line rather than a decision about whether to log.

## Branches and releases

One long-lived branch per minor line: `v0.4.x`. Never a branch named after a release.

```
v0.4.x  →  feature work
main    →  released, tagged work only
```

A release is a single commit, one PR, and one tag. Long-running work is fine as separate commits
during development, squashed before the PR is opened — the checkpoint history is for the author's
benefit, not the repository's.

Release tags are **lightweight** and point at a commit on `main`. Push the tag only after the merge,
so a tag never names a commit that is not on `main`.

`render.yaml` is the source of truth for the deployment. If you change the build, the health check
path, or the environment variables, change it there and mirror it in the dashboard.

## Changing the schema

Migrations are embedded, ordered by filename, and applied once. A migration that has been applied must
never be edited — the runner stores a checksum and refuses to proceed if one changes. Write a new
migration instead.

Take an export before any migration you are unsure about:

```bash
go run ./cmd/verse-export -include-deleted -out verse-$(date +%F).json
```

A Neon branch is a rollback, not a backup.
