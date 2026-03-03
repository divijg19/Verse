# `Verse`

> A daily poetic ritual engine.
> Write. Reflect. Return.

---

### Internal Codename: Hyacinth

Hyacinth represents rebirth through reflection — the internal architecture behind `Verse`.

---

## 🌌 What is `Verse`?

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

## ✨ MVP Features (Hyacinth v0.1)

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

## Minimal Next.js Usage

Next.js is only used where strictly necessary:

* AI proxy endpoints (if required)
* Experimental AI playground
* Potential future auth expansion

It is not the core framework.

`Verse` remains Go-first.

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
* Subtle hyacinth-purple accent

Focus:

> Writing space over interface.

---

# 🚀 Running Locally

```bash id="xq21vd"
# Install dependencies
go mod tidy

# Run migrations
go run cmd/migrate/main.go

# Start server
go run cmd/server/main.go
```

Tailwind (watch mode):

```bash id="t3w67k"
npx tailwindcss -i ./static/css/input.css -o ./static/css/output.css --watch
```

---

# 🔮 Future Roadmap

Hyacinth v1.0.0+:

* AI sentiment analysis
* Theme detection
* Writing evolution tracking

Hyacinth v2.0.0+:

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

# Final Note

`Verse` is not about perfect poems.

It is about showing up daily.

Write.
Reflect.
Return.
