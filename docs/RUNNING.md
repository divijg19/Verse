# Running Verse

Operational reference: what Verse needs in order to run locally, in CI, and on Render, and what to
do when it will not start.

This document covers **running the application**. Architecture rationale, audit findings, risk
tracking, and release planning are engineering records and are not part of this document.

---

## What Verse is

A private, single-author web application for writing poems, prose, fragments, and letters. It
requires a passphrase to enter. It is not a blog, a social network, or a reader-facing site.

---

## Requirements

| Tool | Version | Notes |
|---|---|---|
| Go | 1.26.0 or newer | Declared in `go.mod` |
| templ CLI | v0.3.1020 | Must match `go.mod`. Generated `*_templ.go` files are committed, so the CLI is only needed when editing a `.templ` file |
| Bun | 1.4.2 or newer | Stylesheet build. Matches the version pinned in CI and in the `Dockerfile`; CI asserts the two agree |
| PostgreSQL | 16 or newer | Any instance; Neon is what production uses |

---

## Local setup

```bash
# 1. Start a local database
podman compose up -d          # or: podman run -d -p 5432:5432 \
                              #   -e POSTGRES_USER=verse -e POSTGRES_PASSWORD=verse \
                              #   -e POSTGRES_DB=verse docker.io/library/postgres:16

# 2. Install dependencies and build the stylesheet
bun install --frozen-lockfile
bunx @tailwindcss/cli -i ./static/css/input.css -o ./static/css/output.css --minify

# 3. Export configuration. See "Environment variables" below — the application
#    refuses to start without the two VERSE_ variables.
export DATABASE_URL="postgres://verse:verse@localhost:5432/verse?sslmode=disable"
export VERSE_AUTHORIZATION="choose-a-passphrase"
export VERSE_AUTH_SECRET="at-least-32-characters-of-entropy"

# 4. Create the schema
go run ./cmd/migrate

# 5. Run
go run ./cmd/server
```

Then open <http://localhost:8080> and enter your passphrase.

> **The application does not read a `.env` file.** There is no dotenv loader. If a `.env` exists in
> your working directory, nothing reads it. Every variable must be exported into the process
> environment, or provided by the platform.

---

## Environment variables

### Required

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL connection string. The application will not start without a reachable database |
| `VERSE_AUTHORIZATION` | The authoring passphrase. Compared against the submitted value in constant time |
| `VERSE_AUTH_SECRET` | Key used to sign session and CSRF tokens. **Minimum 32 characters.** Treat it as a secret: changing it invalidates every active session |

**The application refuses to start if either `VERSE_` variable is missing or too short.** This is
deliberate. There is no flag, environment variable, or header that disables authentication.

If you have forgotten the passphrase, set `VERSE_AUTHORIZATION` to a new value and redeploy. If you
have forgotten `VERSE_AUTH_SECRET`, set a new one; all sessions end, and you sign in again.

### Optional

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | Listen port. Platforms such as Render inject this |
| `DB_MAX_CONNS` | `5` | Maximum pooled connections |
| `DB_MIN_CONNS` | `1` | Minimum pooled connections |
| `DB_MAX_CONN_LIFETIME` | `1h` | Maximum connection lifetime |
| `DB_MAX_CONN_IDLE` | `5m` | Idle time before a connection is closed |
| `VERSE_STATIC_DIR` | `static` | Asset directory, resolved relative to the working directory |
| `SERVER_READ_HEADER_TIMEOUT_SEC` | `10` | |
| `SERVER_READ_TIMEOUT_SEC` | `30` | |
| `SERVER_WRITE_TIMEOUT_SEC` | `60` | |
| `SERVER_IDLE_TIMEOUT_SEC` | `120` | |
| `SERVER_SHUTDOWN_TIMEOUT_SEC` | `15` | Grace period for in-flight requests on `SIGTERM` |
| `SERVER_MAX_HEADER_BYTES` | `1048576` | |
| `SERVER_MAX_BODY_BYTES` | `1048576` | Largest accepted request body |

The pool defaults are tuned for a managed connection-limited database such as Neon. They are
deliberately conservative and rarely need changing.

---

## Database and migrations

```bash
go run ./cmd/migrate      # apply migrations
```

Migrations live in `migrations/` and run in filename order.

> **Current limitation, planned for the next release.** The migration runner has no version
> bookkeeping: it re-applies every file on every run. This is currently harmless because every
> statement is written to be idempotent, but a future non-idempotent migration would need the
> versioned runner first. Additionally, the application currently creates its schema on boot
> (`internal/database.EnsureSchema`), so it holds database DDL privileges at runtime. Both are being
> addressed before any schema is changed to a new shape. **Take a backup before running migrations
> against production.**

---

## Tests

The database-backed tests `TRUNCATE TABLE poems`, so they are gated on two things being set
explicitly. Both must be present or the tests **skip** — deliberately, so that a contributor with no
test database still gets a passing `go test ./...`.

```bash
# A dedicated, disposable database. Not the one the application uses.
createdb verse_test

# Gate 1: which database. Not interchangeable with DATABASE_URL — see below.
export VERSE_E2E_DATABASE_URL="postgres://verse:verse@localhost:5432/verse_test?sslmode=disable"

# Gate 2: explicit acknowledgement that these tests delete rows.
export VERSE_E2E_ALLOW_DESTRUCTIVE=1

go test ./... -count=1 -p 1
```

Three things to know:

- **`DATABASE_URL` is not accepted as a fallback, on purpose.** It is the variable the application
  itself boots from, and the local-development steps above tell you to export it. Accepting it here
  meant that a developer who followed those steps and then ran `go test ./...` would empty whatever
  their shell was pointed at. The gate reads only `VERSE_E2E_DATABASE_URL`.
- **The target database name must contain `test`.** This is the backstop that catches a stale
  consent export: a production database is not called `verse_test`, so a misconfigured DSN fails
  loudly. It is a weaker check than the dedicated variable, not a replacement for it.
- **`-p 1` is still required.** The packages still truncate a shared table, so running them
  concurrently against one database races. v0.3.8 gated the `TRUNCATE` but did not give each
  package its own table or database, so this remains until that is done.

The gate lives in `internal/testsupport` and is shared by both test packages, so `cmd/server` and
`tests` cannot drift apart. `git grep TRUNCATE` returns exactly two executable lines, and both sit
inside a helper that re-checks the gate at the moment of destruction rather than trusting the
caller.

CI sets both variables and additionally asserts that **no** test skipped. A skip leaves the exit
status at zero, so without that assertion a misconfigured gate would quietly reduce the job to a
fraction of its coverage and still report green.

Tests authenticate through the real `/login` flow against a TLS test server. Nothing is stubbed, so
a green run means the authentication path works.

---

## Deployment

The production deployment is a Render web service, described by `render.yaml`.

```bash
bun install --frozen-lockfile
bunx @tailwindcss/cli -i ./static/css/input.css -o ./static/css/output.css --minify
go build -tags netgo -ldflags="-s -w" -o verse ./cmd/server
./verse
```

A container build is also provided in the `Dockerfile` and is exercised by CI.

### Before the first deploy of the authenticated build

The service **will not start** unless `VERSE_AUTHORIZATION` and `VERSE_AUTH_SECRET` are set in the
Render dashboard. Set both first, or the service will crash-loop.

Recommended order:

1. Restrict network access to the service (see below).
2. Rotate any database credential that has been shared outside a secret store.
3. Set `VERSE_AUTHORIZATION` and `VERSE_AUTH_SECRET` in the dashboard.
4. Deploy.

### Health check

`/health` returns `200` and `ok`, and touches no database. `render.yaml` points the platform health
check at it. A health probe therefore never depends on database availability.

### Restricting access

The authoring application is private. It is intended to sit behind an access proxy — a
zero-trust identity layer such as Cloudflare Access in front of a dedicated hostname — so that
unauthorised requests never reach the application at all.

The application-level passphrase is the second, independent layer. Neither is treated as sufficient
on its own: a dashboard-level setting is a configuration state that can change without a deploy or
a review, whereas the application refuses to serve content without a valid session.

---

## Operating notes

- **Graceful shutdown.** On `SIGTERM` the server stops accepting connections, allows in-flight
  requests up to `SERVER_SHUTDOWN_TIMEOUT_SEC`, and closes the database pool. There is no need to
  force-kill for a clean deploy.
- **No third-party requests.** Every script and stylesheet is served from this origin. The
  application contacts no external host, so no reader or author metadata leaves the machine.
- **Vendored assets.** `static/js/VENDOR.md` records what is vendored, which version, its licence,
  and its checksum. `static/js/VENDOR.sha256` is verified in CI; a modified vendored file fails the
  build.
- **Health of the commit graph.** A change is validated when its pull request targets `main`, and
  again when it merges into `main`. Superseded runs are cancelled automatically. See
  `.github/workflows/ci.yml`.

---


### Release tags do not trigger a run

Pushing a `v*` tag deliberately starts no workflow. A tag points at a commit its pull request has
already validated and that the merge to `main` validates again, so a tag run would re-test an
identical commit. Because the tag is pushed while the pull request is still open, both events land on
the same commit and GitHub renders every check twice on the pull request, which trains a reader to
ignore the checks panel. To confirm a release commit on demand, run the **CI** workflow manually.

The trade-off is real: pushing a tag at a commit that was never validated runs nothing at all. The
gate for that is the pull request's required check, so a release is only ever tagged from a commit
that passed.

### Render preview instances are disabled and cannot boot

Preview instances are off, and they could not work if they were on. Render does not pass
`sync: false` variables from `render.yaml` to a preview instance, so a preview would have no
`DATABASE_URL` and neither authentication variable, and the service refuses to start without them.
The pull request's container job is the substitute: it builds the image, starts PostgreSQL, and
proves the service both starts and refuses to start unauthenticated.

To see the running application, use the live service. To try an unreleased change, deploy the branch
to the live service deliberately and knowingly.
## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `refusing to start: authentication is not configured` | `VERSE_AUTHORIZATION` or `VERSE_AUTH_SECRET` is unset, or the secret is under 32 characters | Set both in the environment and restart |
| `database connection failed` | `DATABASE_URL` unset, unreachable, or the database is asleep | Check the variable; managed databases need a moment to resume |
| `DATABASE_URL environment variable not set` | The variable is not in the process environment | Export it. A `.env` file in the working directory is **not** read |
| Unstyled page | The stylesheet was not built | `bunx @tailwindcss/cli -i ./static/css/input.css -o ./static/css/output.css --minify` |
| Blank page or missing navigation in the console | The vendored htmx bundle is missing or its checksum changed | Restore `static/js/htmx.min.js`; verify with `cd static/js && sha256sum -c VENDOR.sha256` |
| `templ generate` reports `expected operand` | The `@if` builtin is not usable in this project; conditionals must use the `@If(...)` helper | See `templ/helpers.go` and existing templates for the convention |
| Tests pass but almost nothing ran | No DSN was set, so the database-backed tests skipped silently | Set `VERSE_E2E_DATABASE_URL` and check the skip count in the output |
| Tests fail intermittently | Test packages ran concurrently against one database | Use `-p 1` |
