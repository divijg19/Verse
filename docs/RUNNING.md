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
| `VERSE_AUTHORIZATION` | The authoring passphrase, compared against the submitted value in constant time. **Minimum 16 characters** |
| `VERSE_AUTH_SECRET` | Key used to sign session and CSRF tokens. **Minimum 32 characters.** Treat it as a secret: changing it invalidates every active session |

**Both have a length floor, and the application refuses to start below either.** The passphrase floor
did not exist before v0.4.2, which made it the weaker of the pair: the key needed 32 characters and
the passphrase guarding the entire archive needed one. That is backwards. 16 characters is roughly
what four ordinary words provide, so a memorable passphrase still qualifies. There is no way to
measure a string's entropy, so this is a floor on length and nothing more — `aaaaaaaaaaaaaaaa`
passes it.

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

**Migrations are applied by the service, at startup.** The application no longer creates its schema
from DDL hardcoded in Go — the `.sql` files are the only definition of the schema — but it does run
them itself before serving. You can also run them by hand with the same command, which is
equivalent and safe to repeat.

Migrations live in `migrations/`, run in filename order, and are **embedded into the binaries**, so
the runner needs nothing from disk. A deployed `./migrate` therefore cannot apply SQL from a
different version of the repository than the code it ships with.

### What the runner guarantees

- **Each file runs in its own transaction, and its bookkeeping row is written in that same
  transaction.** A migration that fails partway leaves neither partial schema nor a false record of
  success, and the next run resumes from that file.
- **Already-applied files are verified and skipped**, not re-applied. Each record stores a SHA-256 of
  the file.
- **Editing an applied migration is an error.** The recorded checksum will not match, and the runner
  stops rather than applying one version of a file to a database that already has another. Write a
  new migration instead.
- **Deleting or renaming an applied migration is an error.** Renaming looks exactly like deleting one
  and adding a new one, and the database has already absorbed the old one.
- **A file that is blank after trimming is skipped and never recorded.** A file containing only
  comments is a valid no-op and is recorded like any other migration.

Applied migrations are recorded in `schema_migrations` (`filename`, `checksum`, `applied_at`).

### Migrating an existing database

A database created by an earlier version was built by the service's own boot DDL and has never seen
`schema_migrations`. Running the migrations against it is expected and safe: every statement is
written to be idempotent, so each is a no-op that then gets recorded. **The first deploy of this
version should therefore succeed with no manual step**, and the row that already existed is
untouched. This is covered by a test rather than assumed.

> **Take a backup before running migrations against production.** The runner is idempotent and will
> not re-apply anything, but a new migration that has not been reviewed against real data is still a
> new migration.

### Why migrations run at startup, and not as a deploy step

The right shape is a step between the build and the deploy: the service would need no DDL rights,
and the schema could never be ahead of the code that expects it.

Render provides that hook only for "paid web services, private services, and background workers". A
pre-deploy command needs a **paid compute plan**, not merely a paid workspace, and this service runs
on a free instance. The setting would be accepted by `render.yaml` and then silently never run —
the worst possible failure mode for the step everything else depends on. An earlier draft of this
change had exactly that, and it was removed.

So the service applies its own migrations at startup. Two things follow, and they are worth stating
plainly:

- **The schema can never be ahead of the code.** Both come from the same binary, and the `.sql`
  files are embedded in it.
- **The runtime credential holds DDL rights** for the life of the process, and every start touches
  the database. Applied migrations are verified and skipped, so the touch is a query, not a schema
  change — but the rights are real, and any credential rotation must preserve them.

### If this service moves to a paid compute plan

The better arrangement becomes available, and nothing else changes — the runner, the bookkeeping and
the tests are identical:

1. Add `go build -tags netgo -ldflags="-s -w" -o migrate ./cmd/migrate` to `buildCommand`.
2. Add `preDeployCommand: ./migrate` to `render.yaml`.
3. Drop DDL rights from the credential the service runs with.

Step 3 is safe only once step 2 is in place. Until then the startup migration still needs them, so
revoking first would leave the service unable to start. The startup migration becomes a no-op the
moment the pre-deploy step exists, so there is no window in which both matter.

The container image already ships `/app/migrate` for applying a migration by hand without deploying:

```bash
docker run --rm -e DATABASE_URL="$DATABASE_URL" --entrypoint /app/migrate verse:ci
```

### Concurrency

Migrations are serialized across processes by a Postgres advisory lock, so two instances starting at
once cannot both apply the same file. The applied set is read *after* the lock is taken — reading it
first would let both processes conclude the same migration was pending. The wait is bounded at 30
seconds and reports a clear error rather than appearing to hang.

This is insurance rather than a current need: `WEB_CONCURRENCY` is 1 and there is a single instance.
It matters because a free instance is recycled periodically, so every new process runs the migration
step, and it would matter immediately on any scale-up.

### Login rate limiting

Wrong passphrases are counted per caller and refused once they add up. Without it, the passphrase is
a shared secret on an endpoint reachable from the internet with no other barrier.

| | |
|---|---|
| Threshold | 5 failures |
| Window | 15 minutes, after which failures are forgotten |
| Backoff | 1 minute at the threshold, doubling per further failure, capped at 15 |
| Response | `429` with `Retry-After`, and a body with no numbers in it |
| Key | HMAC-SHA256 of the client address, under `VERSE_AUTH_SECRET` |

Four properties worth knowing before changing any of it:

- **It never sleeps.** A delay holds a connection open and is a cheap way to spend the server's
  capacity, so the backoff lives in the quota and a blocked caller is refused immediately. The
  passphrase comparison already declines to sleep for the same reason.
- **It fails open.** If the limiter cannot reach the database, the refusal is logged and the login
  proceeds. A defence that becomes the outage is worse than no defence. The trade is that an attacker
  who can make the database slow also degrades the limiter.
- **A correct passphrase is still refused while the block holds**, and a successful login clears the
  counter afterwards.
- **It is keyed on an address, and the address is only believed when a proxy is known to be in
  front.** `X-Forwarded-For` is attacker-controlled on a directly reachable service, so it is ignored
  unless Render's `RENDER=true` is present, and then only its rightmost entry is used. A forged
  header therefore cannot move a caller into someone else's bucket.

**The cost, stated plainly:** anyone who learns your address can lock you out of your own authoring
room for up to fifteen minutes. That is accepted. The mitigation is the IP allowlist below, which is
the outer gate; the rate limit is the inner one. If a second proxy is ever placed in front, every
caller through it shares a bucket — the right-side rule cannot be forged, so the answer is to narrow
who can reach the service rather than to widen what is trusted.

### When a login is refused

A refused login answers `403` with the body `forbidden`, whatever the reason, because a client that
cannot produce a valid token has no business learning which of its mistakes it made. The reason goes
to the log instead, and each one reads differently:

| Log line | Means |
|---|---|
| `no synchroniser cookie was sent; request arrived over plain HTTP…` | **A deployment fault, not an attack.** A browser refuses to store a `Secure` cookie on a plain-HTTP page, so the form renders and the submission then cannot be correlated with it. The message says so explicitly |
| `the form carried no synchroniser field` | The template did not render a token. Ours, not the caller's |
| `the synchroniser cookie did not match the form` | A token was sent and was wrong |
| `cross-origin submission` | The attack this defence exists for |
| `rate limited, retry in Ns` | Too many failures; see above |

That first line exists because its absence cost a debugging session. Before v0.4.2 every one of these
produced an identical bare `403` and the log said nothing, so a plain-HTTP deploy fault and a
genuine cross-origin request could not be told apart from the outside.

`Strict-Transport-Security: max-age=31536000` is now sent, so a browser refuses plain HTTP after the
first visit instead of silently discarding the cookie. It is deliberately not qualified with
`includeSubDomains` or `preload`: both are commitments about other hostnames, and this service is a
single host on a platform that serves a wildcard domain.

### Remaining limitation

The public reading site that would use a genuinely read-only publisher credential does not exist
yet, so there is nothing to grant one to. That is the only reason the DDL rights above are still
worth removing.

---

## Backups

Until this existed, the database was the only copy of the work. Reading it in a browser, editing it,
and hand-written SQL were the only ways to get at it, so a provider incident or a mistaken migration
was total loss. Version history (below) protects against a bad edit; this protects against everything
else.

```bash
# Lossless archival copy. Reads DATABASE_URL, like every other command.
go run ./cmd/verse-export -include-deleted -out verse-$(date +%F).json

# For reading rather than restoring.
go run ./cmd/verse-export -format md > verse-$(date +%F).md
```

`verse-export` writes `0600`. The export is the entire body of work, so a world-readable copy is the
same mistake the application exists to avoid.

**JSON is the archival format and is lossless**: re-reading an export and comparing it to the
database yields byte-identical content for every work and every retained revision. It carries the
retained history too, so it is a complete copy rather than a snapshot of current text. A poem is
stored verbatim — nothing is trimmed, translated or truncated on the way out.

**Markdown is not lossless and is not for restoring.** It cannot represent the difference between a
deleted work and a live one, nor the retained revisions, without ceasing to be prose. Use it to read
your own writing; use JSON to keep it.

The browser route at `/export` produces the same document, with `Content-Disposition: attachment`.
It includes soft-deleted work, unlike the command's default: a download clicked in a browser is an
archival act, and an archive that silently omitted the deleted works would be a partial copy of the
thing it claims to preserve. Pass `-include-deleted` to the command for the same completeness.

`verse-export` is a bulk read of everything, so it is the one command where pointing it at something
other than the live database is routine — a replica, or a copy you are verifying. Take a copy before
any migration you are unsure about, rather than relying on the provider's own backups:

```bash
# Neon and most hosted providers can branch a database in seconds. Branch, migrate the branch,
# and only then migrate production. A branch is a rollback, not a backup.
```

---

## Recovering work

Three things can destroy a saved work, and all three are now recoverable:

| What happened | How to undo it |
|---|---|
| A bad edit overwrote it | Open the work, then **History**. Every superseded draft is retained, newest first, and restoring one is itself undoable. |
| It was deleted | **Recycle**, reachable from the library. Soft-deleted work is listed there and can be restored; it keeps its history. |
| The database was lost | `verse-export` from before the incident, restored with `psql`. |

Version history is written by the application, not by a database trigger, so work edited by hand in
SQL has no history. That is a deliberate trade: a trigger would capture every change including
migrations, and would make the write path's transaction considerably harder to reason about.

Retention is unbounded. There is no pruning, and no purge — a version has to be reached through a
restore, which records what it replaced.

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

go test ./... -count=1
```

Three things to know:

- **`DATABASE_URL` is not accepted as a fallback, on purpose.** It is the variable the application
  itself boots from, and the local-development steps above tell you to export it. Accepting it here
  meant that a developer who followed those steps and then ran `go test ./...` would empty whatever
  their shell was pointed at. The gate reads only `VERSE_E2E_DATABASE_URL`.
- **The target database name must contain `test`.** This is the backstop that catches a stale
  consent export: a production database is not called `verse_test`, so a misconfigured DSN fails
  loudly. It is a weaker check than the dedicated variable, not a replacement for it.
- **No `-p 1` is needed.** Each test package that touches the database has its own schema, so
  they run concurrently without truncating one another. The two are `verse_t_tests` and
  `verse_t_cmdserver`, and the tables in them are distinct relations rather than one table
  reached two ways. Until v0.4.3 they shared the default schema and `-p 1` was the only
  thing preventing the conflict — a constraint that had to be remembered locally and could be
  forgotten without any test failing.

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

### `render.yaml` is the source of truth; the dashboard must mirror it

The two have already diverged once. The dashboard's build command was a copy of an older revision of
`render.yaml`, so Render was building with an unlocked dependency install where the repository
specified `--frozen-lockfile` — and nothing noticed, because nothing compared them.

When changing how the service is built, change `render.yaml` **and** the dashboard setting, in the
same commit. CI checks the repository's three build paths against each other; it cannot see the
dashboard.

### Toolchain: Bun is pinned, Go cannot be

| | CI and `Dockerfile` | Render |
|---|---|---|
| Bun | `1.4.2`, asserted equal across all three paths | `1.4.2`, via `BUN_VERSION` in `render.yaml` |
| Go | `1.26.x` from `go.mod`, asserted equal to the `Dockerfile` | **latest stable 1.x** — not pinnable |

Render's default Bun depends on when the service was created, and this one defaulted to `1.3.4` while
the repository was on `1.3.5` and then `1.4.2`. Production was therefore building the stylesheet with
a package manager version no other build path used. `BUN_VERSION` fixes that, and CI now fails if the
three disagree — including when the pin is *absent*, which was the state that produced `1.3.4`.

Go is a genuine, accepted difference. Render's native Go runtime always tracks the latest stable 1.x
and, in their words, "you can't pin to a specific Go version unless you deploy a Docker image." CI
asserts the `Dockerfile` matches `go.mod`; it cannot make Render match, and asserting a value Render
ignores would be a gate that is permanently red or ignored. A newer Go toolchain is backward
compatible, so this is recorded rather than engineered around. Deploying the `Dockerfile` on Render
would close it.

### Before the first deploy of the authenticated build

The service **will not start** unless `VERSE_AUTHORIZATION` and `VERSE_AUTH_SECRET` are set in the
Render dashboard. Set both first, or the service will crash-loop.

Recommended order:

1. Restrict network access to the service (see below).
2. Rotate any database credential that has been shared outside a secret store.
3. Set `VERSE_AUTHORIZATION` and `VERSE_AUTH_SECRET` in the dashboard.
4. Deploy.

### Health check

`/health` returns `200` and `ok` when the database is reachable, and `503` when it is not.
`render.yaml` points the platform health check at it.

This is a change of behaviour, and deliberately so. It previously answered `200` without touching the
database, on the reasoning that a probe should not depend on database availability. The result was
worse than the problem it avoided: a total Postgres outage left the platform polling a service that
could not serve a single page, so nothing restarted, nothing alerted, and the log showed an unbroken
stream of `200` for the whole incident. A probe that cannot fail is read as evidence, so it must be
able to fail.

If the database is the thing you most need to stay up regardless, configure the platform to treat
`/health` as a liveness probe only and monitor readiness separately — but do not restore the
unconditional `200` without understanding that you are disabling the restart and the signal.

`GET /health` returns a body (`ok`, or `unavailable` when degraded). `HEAD /health` returns the same
status with no body. The reason for a `503` is logged with the request ID and never returned: the
probe is unauthenticated, and a connection error carries the database host and user.

Note what a `503` does and does not mean. The service refuses to start without a database —
`RequireSchema` runs at boot — so an unreachable database at startup is a failed deploy, visible in
the logs, not a `503`. A `503` therefore means the database was reachable at boot and has since
become unreachable, which on Render's free tier is the case worth alerting on: a restarting or
crashed instance comes back healthy, a `503` is a live instance that can no longer serve.

### Diagnosing a refusal

A refused request answers `403` with a body of exactly `forbidden`, and that is a contract. A caller
that cannot produce a valid token has no business being told which of its mistakes to correct —
distinguishing the reasons is the entire value of the synchroniser token.

Every response carries `X-Request-Id`, including refusals. `middleware.RequestID` honours an inbound
`X-Request-Id`, so on Render this is the platform's own identifier, and the same value appears in the
platform's request log. That is what lets a bug report be turned into a log line.

Every refusal is logged with its reason and request ID, on both the login path and the authenticated
mutation path.

To have the reason echoed in a response header as well, set `VERSE_DEBUG_LOGIN=1`. This is intended
for a deployment you control, and it reveals only a fixed word from the application's own vocabulary —
never a value derived from the request — so it cannot be used as a reflector. The body stays
`forbidden` regardless. Leave it unset in production unless you are actively debugging; the log
already carries the reason.

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
| `refusing to start: authentication is not configured` | `VERSE_AUTHORIZATION` or `VERSE_AUTH_SECRET` is unset, or either is under its minimum (16 and 32) | Set both in the environment and restart. The message states both minimums |
| `too many failed attempts` (HTTP 429) | The caller is rate limited; the body is deliberately numberless | Wait for `Retry-After`. If it is you and you have not been guessing, see the rate-limit section above || `refusing to start: authentication is not configured` | `VERSE_AUTHORIZATION` or `VERSE_AUTH_SECRET` is unset, or the secret is under 32 characters | Set both in the environment and restart |
| `database connection failed` | `DATABASE_URL` unset, unreachable, or the database is asleep | Check the variable; managed databases need a moment to resume |
| `DATABASE_URL environment variable not set` | The variable is not in the process environment | Export it. A `.env` file in the working directory is **not** read |
| `database migration failed: ... another process has held the migration lock` | Two instances are migrating at once, or one died holding the lock | Usually transient; the wait is bounded at 30s. If it persists, check for an instance stuck in `pg_locks` |
| `database schema is not ready: the poems table does not exist` | The migration runner reported success but the `poems` table is absent | A real inconsistency between the runner and the application; check `schema_migrations` and the migration files |
| `migration <file> was modified after it was applied` | An already-applied `.sql` file was edited | Restore the original file, or write a new migration. Do not edit history |
| `these migrations are recorded as applied but no longer exist` | An applied `.sql` file was deleted or renamed | Restore it. Renaming an applied migration is indistinguishable from deleting it |
| `apply migration <file>: ...` | The SQL in a migration failed | Nothing was recorded and nothing was left behind; fix the file and re-run |
| Production stylesheet differs from CI | Render's Bun drifted from the repository's | Check `BUN_VERSION` on the service; CI asserts all three build paths agree |
| Deploy used a stale or different build command | The dashboard diverged from `render.yaml` | Re-sync the dashboard from `render.yaml`; CI cannot detect this |
| Unstyled page | The stylesheet was not built | `bunx @tailwindcss/cli -i ./static/css/input.css -o ./static/css/output.css --minify` |
| Blank page or missing navigation in the console | The vendored htmx bundle is missing or its checksum changed | Restore `static/js/htmx.min.js`; verify with `cd static/js && sha256sum -c VENDOR.sha256` |
| `templ generate` reports `expected operand` | The `@if` builtin is not usable in this project; conditionals must use the `@If(...)` helper | See `templ/helpers.go` and existing templates for the convention |
| Tests pass but almost nothing ran | No DSN was set, so the database-backed tests skipped silently | Set `VERSE_E2E_DATABASE_URL` and check the skip count in the output |
| Tests fail intermittently with a truncated or missing row | Two packages shared one `poems` table | Should be impossible as of v0.4.3, since each package has its own schema. If it recurs the isolation has been broken: check that `verse_t_tests` and `verse_t_cmdserver` each exist and hold their own `poems` |
