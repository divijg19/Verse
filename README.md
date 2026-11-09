# `Verse`

> A private daily writing practice.
> Write. Reflect. Return.

---

`Verse` is a private, daily web application built for disciplined poetic practice: a personal
writing ritual, not a social platform, publishing tool, or productivity dashboard.

---

## Core Philosophy

`Verse` is designed around three principles:

1. Writing comes first.
2. Reflection follows.
3. Analysis is optional.

The interface stays out of the way: no feeds, no recommendations, no algorithmic interference.

---

## Features

- Daily poem editor
- Version history — every superseded draft is retained, and a restore is itself undoable
- Recycle — soft-deleted work is listed and can be brought back
- Export — a lossless JSON copy of the whole library, plus a Markdown rendering for reading
- Streak tracking, counted in UTC days
- Monthly activity heatmap
- `Caelum` (random prompt engine)
- Single-author, passphrase-protected access

---

## `Caelum`

`Caelum` is the inspiration surface within `Verse`.

It provides exactly one thing: a button that returns one of ten conceptual, non-imperative
prompts, held as a static list in `internal/services/prompts.go` and served as an HTML fragment
by `handlers.PromptHandler`. There is no generation, no model, and no network call — the whole
feature is ten strings and `math/rand/v2`.

---

# Architecture Overview

`Verse` uses server-rendered HTML with minimal JavaScript.

Primary stack:

- Go (core backend)
- Templ (server-side rendering)
- HTMX (dynamic interactions)
- TailwindCSS (styling)
- PostgreSQL (data layer)

---

# Tech Stack (Detailed)

## Backend

- Go 1.27 (declared in `go.mod`)
- Chi router
- pgx v5 (PostgreSQL driver)
- Hand-written SQL. There is no query generator: every statement is in `internal/services`, and
  `tests/readme_schema_test.go` checks the schema block below against the live database
- `internal/migrate`, which embeds the `.sql` files and applies them in filename order with a
  recorded checksum per file. Not a migration framework — see `RUNNING.md`

Why Go:

- Single-binary deployment
- Explicit error handling
- A strong standard library

---

## Templ (Server Rendering)

Templ generates type-safe HTML components.

Used for:

- Editor page
- Library, dashboard, Caelum, share and recycle surfaces
- Layout system, with the navigation rendered out of band on an HTMX swap
- Reusable UI components

---

## HTMX

HTMX handles:

- Update a work without a full page reload
- Swap a surface without a full page reload
- Replace the heatmap for a chosen month
- Retain and restore a superseded revision

Minimal JS.
Declarative interactivity.

---

## TailwindCSS

Used via:

- Standalone CLI
- Bun scripts and the container build

Provides:

- Dark theme
- Typography control
- Minimal aesthetic

---

# Project Structure

```
verse/
|
├── cmd/
|   ├── server/        the application, and the migration run at its boot
|   ├── migrate/       the standalone migration runner, same migrations
|   ├── publish/       the static public-site builder
|   └── verse-export/  the standalone export command
|
├── internal/
|   ├── server/        routes, security headers, sessions, CSRF
|   ├── handlers/      HTTP handlers; one file per surface
│   ├── services/      the application-domain SQL: dashboard.go, library.go, prompts.go
|   ├── database/      connection, pool configuration, schema assertion
|   ├── migrate/       the migration runner: checksums, advisory lock, timeouts
|   ├── models/        domain types
|   ├── presenters/    content flattening and truncation for rendering
|   ├── export/        the export writers, JSON and Markdown
|   ├── publish/       the public site renderer, validator, and atomic writer
|   ├── htmx/          the one HTMX-request predicate
|   ├── clock/         the injectable clock
|   └── testsupport/   disposable-DSN gate and scratch schemas
|
├── templ/             one .templ per surface, each with its generated Go counterpart
|   ├── layout.templ   shell, navigation, and the CSP-relevant script tags
|   ├── editor.templ   the editor and its Focus Mode
|   ├── heatmap.templ  the month grid
|   ├── public.templ   the static public site: index, archive, work pages, and 404
|   └── security.go    the CSRF field the mutating routes require
|
├── static/
|   ├── css/input.css  Tailwind entry point, with its @source directives
|   └── js/            navigation.js, editor.js, and vendored htmx
|
├── migrations/        001 through 007, embedded by the migrations package and used by the
|                     server, cmd/migrate, and cmd/publish
|
├── db/                role bootstrap: the least-privilege grants, applied once by an operator
|                     and deliberately not a migration, because it needs a credential the
|                     service should not hold
|
├── Dockerfile         the second build path, exercised by CI
├── go.mod
├── RUNNING.md         operations, environment variables, the migration contract
└── README.md
```

`internal/services` holds all the application-domain SQL — poems, versions, and the dashboard
aggregates. The rate limiter, export writers, migration bookkeeping, and the boot-time
`to_regclass` assertion keep their queries next to the code that owns them.

---

# Database Schema

There is no `users` table, no registration, and no multi-tenancy. Authentication is a single
passphrase compared against a configured value; the poems belong to whoever holds it.

The block below is checked against the live schema by `TestReadmeSchemaMatchesTheDatabase`, so it
cannot drift again silently. Types are documented by alias (`timestamptz` for
`timestamp with time zone`); the test compares table and column names, and checks each documented
type is one the schema actually uses.

<!-- documented-schema:begin -->

```sql
table poems {
    id           uuid        -- primary key
    content      text        -- the work itself, stored verbatim
    created_at   timestamptz -- nullable: the column has a default but is not declared NOT NULL
    deleted_at   timestamptz -- nullable: set by a soft delete, NULL while the work is live
    published_at timestamptz -- nullable: set by an explicit publish, NULL while the work is a draft
}

table poem_versions {
    id           uuid        -- primary key
    poem_id      uuid        -- references poems(id); the work this revision belongs to
    content      text        -- what the work said immediately before an edit
    recorded_at  timestamptz -- when that edit happened; human-facing, not used for ordering
    seq          bigint      -- identity; the history is ordered by this, because recorded_at can tie
}

table login_attempts {
    subject        bytea                 -- HMAC of the caller address; never the address itself
    failures       integer
    first_failure  timestamptz
    blocked_until  timestamptz          -- nullable: NULL means not blocked
}

table schema_migrations {
    filename   text       -- primary key
    checksum   text       -- sha256 of the file, so an applied migration cannot be edited
    applied_at timestamp
}
```

<!-- documented-schema:end -->

Notes:

**Every timestamp in the schema is `timestamptz` except one.** `schema_migrations.applied_at` is the
exception, and it is left as `timestamp` on purpose: nothing reads it — the runner selects only
`filename` and `checksum` — it records when a migration ran rather than anything the application
interprets, and the runner creates that table with `IF NOT EXISTS` against databases that may predate
it, so its column definitions are not ours to change incompatibly.

**`poems.created_at` is nullable.** It has a default but was never declared `NOT NULL`, so a row
inserted with an explicit `NULL` is representable. The conversion to `timestamptz` left such a row as a
`NULL` rather than inventing a creation instant for it, and a work with no creation instant is
absent from the heatmap and from the streak, which is visible and recoverable. Promoting the column to
`NOT NULL` is a separate decision and is not done here.

**A day is a UTC day.** `DATE(created_at)` would resolve in the session's time zone, so both dashboard
queries say `AT TIME ZONE 'UTC'` and the connection pool pins every session to UTC as well. A poem
written at 20:00 UTC is already the next calendar day at `+05:30`; without this it would be credited to
the following day, and a real streak would read as broken. The clause and the pin are deliberately
redundant — the clause states the intent for whoever reads the query, the pin covers a future query
written without it.

**`poems.published_at` is how a work becomes public, and it is `NULL` on every existing row.**
`007_published_at.sql` added it for the author/viewer split. It has no `DEFAULT`, so the migration
backfills existing rows to `NULL` without a rewrite and publishes nothing. Publication is an explicit
act by the author, and no application path writes the column yet.

It follows `deleted_at` rather than introducing a `status` enum, because twelve query sites already
read `deleted_at IS NULL` and a second idiom for the same kind of fact is a second thing to get wrong.
The publisher's filter is `published_at IS NOT NULL AND deleted_at IS NULL` — both halves
load-bearing, since a work can be published and then deleted — and it lives in exactly one place,
`models.PublishedFilter`.

**The authoring app is unaffected by it.** Every read in the author's library filters `deleted_at IS
NULL` only, so a draft is a row the author sees, opens, and can search for exactly as before. The
public site is built by `cmd/publish` from the published set; see `internal/publish`.

**`poem_versions` has no pruning.** Nothing updates or deletes a row, and there is no purge path.
Restoring a revision is an ordinary edit, which records what it replaced, so the history is
append-only and a restore can itself be undone.

---

# Streak Logic

Computed dynamically.

Algorithm:

1. Fetch poem dates
2. Sort descending
3. Count consecutive days
4. Reset on gap > 1 day

No cached streak field.

---

# Design Direction

Default:

- Dark mode
- Serif typography for poems
- Minimal UI chrome
- Subtle violet accent

Focus:

> Writing space over interface.

---

# Running Locally

```bash
# 1. Start a database
podman compose up -d

# 2. Dependencies and stylesheet
bun install --frozen-lockfile
bunx @tailwindcss/cli -i ./static/css/input.css -o ./static/css/output.css --minify

# 3. Configuration — all three are required
export DATABASE_URL="postgres://verse:verse@localhost:5432/verse?sslmode=disable"
export VERSE_AUTHORIZATION="choose-a-passphrase"
export VERSE_AUTH_SECRET="at-least-32-characters-of-entropy"

# 4. Apply the migrations — optional; cmd/server runs the same runner at boot.
#    Use this step to migrate without starting the service (deploy scripts, CI).
go run ./cmd/migrate

# 5. Run
go run ./cmd/server
```

Open <http://localhost:8080> and enter your passphrase.

`VERSE_AUTHORIZATION` and `VERSE_AUTH_SECRET` are mandatory: the server refuses to start without
them, by design. There is no way to disable authentication.

**Verse does not read a `.env` file.** Every variable must be exported into the process
environment. Tailwind watch mode:

```bash
bunx @tailwindcss/cli -i ./static/css/input.css -o ./static/css/output.css --watch
```

Full operational reference — environment variables, migrations, tests, deployment, and
troubleshooting — is in [`RUNNING.md`](RUNNING.md).

---

# Future Roadmap

- Local-first mode
- Offline support
- Desktop wrapper (Tauri)

---

# Why `Verse` Exists

`Verse` exists to make daily writing a habit: to create, reflect, and keep a record over time.
It is optimized for depth, not virality.

---

# License

Private project.
