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

## ✨ MVP Features (v0.1)

* Daily poem editor
* Mood tagging
* Streak tracking
* Calendar archive
* `Caelum` (random prompt engine)
* Private-first architecture

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

# 🗄 Database Schema (MVP)

## users

* id (uuid)
* email
* created_at

## poems

* id (uuid)
* user_id
* content (text)
* mood (enum)
* prompt_used (nullable text)
* created_at

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

# 4. Create the schema
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
