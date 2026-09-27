# `Verse`



> A daily poetic ritual engine.
> Write. Reflect. Return.

---


`Verse` is a private, daily web application built for disciplined poetic practice.

It is not:

* A social writing platform
* A publishing tool
* A productivity dashboard

It is:

> A personal cognitive ritual system for daily poetry.

Minimal. Focused. Intentional.

---

## 🧭 Core Philosophy

`Verse` is designed around three principles:

1. Writing comes first.
2. Reflection follows.
3. Analysis is optional.

No clutter.
No noise.
No algorithmic interference.

---

## ✨ Features

* Daily poem editor
* Version history — every superseded draft is retained, and a restore is itself undoable
* Recycle — soft-deleted work is listed and can be brought back
* Export — a lossless JSON copy of the whole library, plus a Markdown rendering for reading
* Streak tracking
* Calendar archive
* `Caelum` (random prompt engine)
* Private-first architecture

There is **no mood tagging**. It was listed here from the first commit and never built; there has
never been a `mood` column.

---

## 🌌 `Caelum`

`Caelum` is the inspiration engine within `Verse`.

It provides:

* Random poetic prompts
* Constraint-based writing seeds
* Emotional triggers

Future versions will integrate AI-assisted generation.

---

# 🏗 Architecture Overview

`Verse` intentionally minimizes JavaScript-heavy frameworks.

Primary stack:

* Go (core backend)
* Templ (server-side rendering)
* HTMX (dynamic interactions)
* TailwindCSS (styling)
* PostgreSQL (data layer)
* Dart + Jaspr (interactive UI islands)
* Minimal Next.js (only where necessary)

---

# 🧱 Tech Stack (Detailed)

## Backend

* Go 1.22+
* Chi router
* pgx (PostgreSQL driver)
* sqlc or manual queries
* Goose or Atlas for migrations

Why Go:

* Performance
* Explicitness
* Long-term architectural alignment

---

## Templ (Server Rendering)

Templ generates type-safe HTML components.

Used for:

* Editor page
* Calendar page
* Layout system
* Reusable UI components

---

## HTMX

HTMX handles:

* Save poem without full page reload
* Load prompt dynamically
* Update streak counter
* Fetch calendar entries

Minimal JS.
Declarative interactivity.

---

## TailwindCSS

Used via:

* Standalone CLI
* Integrated into Go build pipeline

Provides:

* Dark theme
* Typography control
* Minimal aesthetic

---

## Dart + Jaspr (Selective UI Islands)

Used only where reactive UI is valuable.

Planned use cases:

* Mood selector animation
* Future analytics dashboard
* AI analysis visualizations

Jaspr compiles to lightweight web components embedded in Templ layouts.

---

# 📁 Project Structure

```id="zkq92v"
verse/
│
├── cmd/
│   └── server/
│        └── main.go
│
├── internal/
│   ├── handlers/
│   ├── database/
│   ├── models/
│   ├── services/
│   │    ├── streak.go
│   │    ├── prompts.go
│   │    └── mood.go
│   └── middleware/
│
├── templ/
│   ├── layout.templ
│   ├── editor.templ
│   ├── calendar.templ
│   ├── components/
│   │    ├── streak.templ
│   │    ├── mood_selector.templ
│   │    └── caelum_button.templ
│
├── static/
│   ├── css/
│   ├── js/
│   └── wasm/
│
├── jaspr/
│   └── mood_island/
│
├── migrations/
│
├── go.mod
└── README.md
```

---

# 🗄 Database Schema

There is no `users` table, no registration, and no multi-tenancy. Authentication is a single
passphrase compared against a configured value; the poems belong to whoever holds it. This section
used to describe a `users` table and a `poems.mood` enum, none of which have ever existed.

The block below is checked against the live schema by `TestReadmeSchemaMatchesTheDatabase`, so it
cannot drift again silently. Types are documented by alias (`timestamptz` for
`timestamp with time zone`); the test compares table and column names, and checks each documented
type is one the schema actually uses.

<!-- documented-schema:begin -->
```sql
table poems {
    id           uuid        -- primary key
    content      text        -- the work itself, stored verbatim
    created_at   timestamp   -- nullable: the column has a default but is not declared NOT NULL
    deleted_at   timestamp   -- nullable: set by a soft delete, NULL while the work is live
}

table poem_versions {
    id           uuid        -- primary key
    poem_id      uuid        -- references poems(id); the work this revision belongs to
    content      text        -- what the work said immediately before an edit
    recorded_at  timestamp   -- when that edit happened; human-facing, not used for ordering
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

Two notes worth reading rather than skimming:

**`poems.created_at` is `timestamp`, not `timestamptz`.** Every other timestamp in the schema is
`timestamptz`, so this is an inconsistency rather than a decision. Normalising it is a rewrite of
the `poems` table — the one table holding the work — so it is deliberately not bundled into a
release that also starts writing to it. Take an export first.

**`poem_versions` has no pruning.** Nothing updates or deletes a row, and there is no purge path.
Restoring a revision is an ordinary edit, which records what it replaced, so the history is
append-only and a restore can itself be undone.

---

# 🔁 Streak Logic

Computed dynamically.

Algorithm:

1. Fetch poem dates
2. Sort descending
3. Count consecutive days
4. Reset on gap > 1 day

No cached streak field.

---

# 🎨 Design Direction

Default:

* Dark mode
* Serif typography for poems
* Minimal UI chrome
* Subtle violet accent

Focus:

> Writing space over interface.

---

# 🚀 Running Locally

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

  # 4. Apply the migrations — required before the service will start
  go run ./cmd/migrate

# 5. Run
go run ./cmd/server
```

Open <http://localhost:8080> and enter your passphrase.

`VERSE_AUTHORIZATION` and `VERSE_AUTH_SECRET` are mandatory: the server refuses to start without
them, by design. There is no way to disable authentication.

**Verse does not read a `.env` file.** Every variable must be exported into the process
environment. Tailwind watch mode:

```bash id="t3w67k"
bunx @tailwindcss/cli -i ./static/css/input.css -o ./static/css/output.css --watch
```

Full operational reference — environment variables, migrations, tests, deployment, and
troubleshooting — is in [`docs/RUNNING.md`](docs/RUNNING.md).

---

# 🔮 Future Roadmap

v1.0.0+:

* AI sentiment analysis
* Theme detection
* Writing evolution tracking

v2.0.0+:

* Local-first mode
* Offline support
* Desktop wrapper (Tauri)

Long-term:

`Verse` integrates into a broader cognition ecosystem.

---

# 🧠 Why `Verse` Exists

`Verse` exists to:

* Encourage disciplined creation
* Externalize emotion
* Track personal growth
* Preserve authenticity

It is not optimized for virality.

It is optimized for depth.

---

# 📜 License

Private project.
