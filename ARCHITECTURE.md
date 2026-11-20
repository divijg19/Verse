# Verse architecture

This document describes how Verse is built and why. For setup, environment variables, migrations,
deployment and troubleshooting, see [RUNNING.md](RUNNING.md).

---

## Architecture Overview

Verse uses server-rendered HTML with minimal JavaScript.

Primary stack:

- Go (core backend)
- Templ (server-side rendering)
- HTMX (dynamic interactions)
- TailwindCSS (styling)
- PostgreSQL (data layer)

---

## Tech Stack (Detailed)

### Backend

- Go 1.27 (declared in `go.mod`)
- Chi router
- pgx v5 (PostgreSQL driver)
- Hand-written SQL. There is no query generator: every statement is in `internal/services`, and
  `tests/readme_schema_test.go` checks the schema block below against the live database
- `internal/migrate`, which embeds the `.sql` files and applies them in filename order with a
  recorded checksum per file. Not a migration framework — see [RUNNING.md](RUNNING.md)

Why Go:

- Single-binary deployment
- Explicit error handling
- A strong standard library

### Templ (Server Rendering)

Templ generates type-safe HTML components.

Used for:

- Editor page
- Library, dashboard, Caelum, share and recycle surfaces
- Layout system, with the navigation rendered out of band on an HTMX swap
- Reusable UI components

### HTMX

HTMX handles:

- Update a work without a full page reload
- Swap a surface without a full page reload
- Replace the heatmap for a chosen month
- Retain and restore a superseded revision

Minimal JS. Declarative interactivity. The reader's site has exactly one script; the authoring site
has four, each compiled from TypeScript in `web/ts` by `scripts/build_js.ts`.

### TailwindCSS

Used via:

- Standalone CLI
- Bun scripts and the container build

Provides:

- Dark theme
- Typography control
- Minimal aesthetic

---

## Project Structure

```
verse/
|
├── cmd/
|   ├── server/        the application, and the migration run at its boot
|   ├── migrate/       the standalone migration runner, same migrations
|   ├── publish/       the static public-site builder
|   ├── verse-export/  the standalone export command
|   ├── verse-publish/ writes the published set and asks for a deploy
|   └── verse-artifact/ fetches the published set the deployment builds from
|
├── internal/
|   ├── server/        routes, security headers, sessions, CSRF, guest sessions
|   ├── handlers/      HTTP handlers; one file per surface
|   ├── services/      the application-domain SQL: poems, versions, dashboard, private and public links
|   ├── database/      connection, pool configuration, schema assertion
|   ├── migrate/       the migration runner: checksums, advisory lock, timeouts
|   ├── models/        domain types
|   ├── presenters/    content flattening and truncation for rendering
|   ├── export/        the export writers, JSON and Markdown
|   ├── publish/       the public site renderer, validator, and atomic writer
|   ├── origin/        origin derivation for private and public-link URLs
|   ├── htmx/          the one HTMX-request predicate
|   ├── clock/         the injectable clock
|   └── testsupport/   disposable-DSN gate and scratch schemas
|
├── templ/             one .templ per surface, each with its generated Go counterpart
|   ├── layout.templ   shell, navigation, and the CSP-relevant script tags
|   ├── editor.templ   the editor and its Focus Mode
|   ├── guest.templ    the private-link editor and its submission confirmation
|   ├── public_submit.templ  the unauthenticated public submission form
|   ├── heatmap.templ  the month grid
|   ├── public.templ   the static public site: index, archive, work pages, and 404
|   └── security.go    the CSRF field the mutating routes require
|
├── web/ts/            TypeScript sources for editor.ts, navigation.ts, share.ts, guest.ts, public.ts
├── static/
|   ├── css/input.css  Tailwind entry point, with its @source directives
|   └── js/            compiled editor.js, navigation.js, share.js, guest.js, and vendored htmx
|
├── migrations/        embedded SQL migrations, applied by the server, cmd/migrate and cmd/publish
|
├── db/                role bootstrap: the least-privilege grants, applied once by an operator
|                      and deliberately not a migration, because it needs a credential the
|                      service should not hold
|
├── Dockerfile         the second build path, exercised by CI
├── go.mod
├── ARCHITECTURE.md    this document
├── RUNNING.md         operations, environment variables, the migration contract
└── README.md          what Verse is, and where to find the rest
```

`internal/services` holds all the application-domain SQL — poems, versions, the dashboard
aggregates, and the private-link drafts and proposals. The rate limiter, export writers, migration
bookkeeping, and the boot-time `to_regclass` assertion keep their queries next to the code that owns
them.

---

## Anonymous write routes

`GET /p/{token}` and `POST /p/{token}/submit` are the only unauthenticated write surface. A public
link is a token for one published work; every submission is appended to `public_submissions` and
reviewed read-only in History. The route group carries no-store/noindex, its token is redacted from
logs, both entry and submit are rate-limited per address, and a double-submit CSRF cookie guards the
form. The reader's static site is unchanged.

## Database Schema

There is no `users` table, no registration, and no multi-tenancy. Authentication is a single
passphrase compared against a configured value; the poems belong to whoever holds it.

The block below is checked against the live schema by `TestDocumentedSchemaMatchesTheDatabase`, so it
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

table private_links {
    id          uuid        -- primary key
    poem_id     uuid        -- references poems(id); the one work this link can edit
    token_hash  text        -- sha256 of the raw token; the raw token is shown once and never stored
    created_at  timestamptz
    expires_at  timestamptz -- eight hours after creation; no renewal
    revoked_at  timestamptz -- nullable: NULL means the link still works
}

table work_proposals {
    id          uuid        -- primary key
    poem_id     uuid        -- references poems(id)
    link_id     uuid        -- references private_links(id); which link carried the suggestion
    branch      text        -- 'primary', or an opaque id for an explicitly parallel submission
    guest_name  text        -- optional attribution, "Anonymous" when left blank
    content     text        -- the submitted rewrite; never written to poems by the guest
    created_at  timestamptz
    updated_at  timestamptz -- last submission on this branch
    applied_at  timestamptz -- nullable: NULL until the author applies it to the draft
}

table guest_drafts {
    link_id     uuid        -- part of the primary key; references private_links(id)
    branch      text        -- 'primary', or 'alt:<uuid>'; one continuous body per branch
    poem_id     uuid        -- references poems(id)
    guest_name  text        -- optional attribution, remembered between visits
    content     text        -- one continuous body, autosaved for the link's eight-hour window
    base_hash   text        -- sha256 of the body at the last send, for change detection
    updated_at  timestamptz
}

table public_links {
    id          uuid        -- primary key
    poem_id     uuid        -- references poems(id); one link per published work
    token_hash  text        -- sha256 of the raw token; shown once, never stored
    created_at  timestamptz
    revoked_at  timestamptz -- nullable: NULL means the link still accepts submissions
}

table public_submissions {
    id          uuid        -- primary key
    link_id     uuid        -- references public_links(id)
    poem_id     uuid        -- references poems(id)
    guest_name  text        -- optional attribution
    content     text        -- appended verbatim; never applied to the work automatically
    created_at  timestamptz
}

table guest_requests {
    subject       bytea       -- part of the key; HMAC of the caller address
    kind          text        -- entry, editor or public; part of the key
    window_start  timestamptz
    count         integer
}

table login_attempts {
    subject        bytea                 -- HMAC of the caller address; never the address itself
    failures       integer
    first_failure  timestamptz
    blocked_until  timestamptz           -- nullable: NULL means not blocked
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

**`poems.published_at` is how a work becomes public.** `007_published_at.sql` added it for the
author/viewer split. It has no `DEFAULT`, so the migration backfilled existing rows to `NULL` without
a rewrite and published nothing. Publication is an explicit act by the author.

It follows `deleted_at` rather than introducing a `status` enum, because twelve query sites already
read `deleted_at IS NULL` and a second idiom for the same kind of fact is a second thing to get wrong.
The publisher's filter is `published_at IS NOT NULL AND deleted_at IS NULL` — both halves
load-bearing, since a work can be published and then deleted — and it lives in exactly one place,
`models.PublishedFilter`.

**Publishing is an act, not a statement.** The work's own screen carries the control, and it says
which state the work is in. The published set then travels as a file: the authoring environment
writes it out and stores it, and the deployment environment reads it and builds from it. That is
what keeps the database credential out of the build environment.

**`poem_versions` has no pruning.** Nothing updates or deletes a row, and there is no purge path.
Restoring a revision is an ordinary edit, which records what it replaced, so the history is
append-only and a restore can itself be undone.

---

## Streak Logic

Computed dynamically.

Algorithm:

1. Fetch poem dates
2. Sort descending
3. Count consecutive days
4. Reset on gap > 1 day

No cached streak field.

---

## Design Direction

Default:

- Dark mode
- Serif typography for poems
- Minimal UI chrome
- Subtle violet accent

Focus:

> Writing space over interface.

---

## Caelum

`Caelum` is the inspiration surface within Verse.

It provides exactly one thing: a button that returns one of ten conceptual, non-imperative
prompts, held as a static list in `internal/services/prompts.go` and served as an HTML fragment
by `handlers.PromptHandler`. There is no generation, no model, and no network call — the whole
feature is ten strings and `math/rand/v2`.

---

## Future Roadmap

- Local-first mode
- Offline support
- Desktop wrapper (Tauri)

---

## Why Verse Exists

Verse exists to make daily writing a habit: to create, reflect, and keep a record over time.
It is optimized for depth, not virality.
