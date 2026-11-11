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

| Tool       | Version         | Notes                                                                                                                              |
| ---------- | --------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| Go         | 1.27.0 or newer | Declared in `go.mod`                                                                                                               |
| templ CLI  | v0.3.1100       | Must match `go.mod`. Generated `*_templ.go` files are committed, so the CLI is only needed when editing a `.templ` file            |
| Bun        | `latest`        | Stylesheet build. CI, the `Dockerfile`, Render, and Workers Builds all name the same policy; CI asserts the repository paths agree |
| Node       | 24              | Workers Builds only, pinned by `.node-version`. Not needed for local development                                                   |
| PostgreSQL | 16 or newer     | Any instance; Neon is what production uses. Put it in the same region as the service -- see below                                  |
| TypeScript | 7.x             | Type checking only, and only through Bun's built-in `bun check` -- there is no `tsc` dependency                                    |

---

## Local setup

Four commands, all invoked through Bun:

```bash
# 1. Once per checkout: start a database and write the .env file.
bun dev:setup

# 2. Both sites, at once.
bun dev

# 3. One site on its own.
bun dev:author
bun dev:public
```

`bun dev` starts both servers together and prints where each one is:

```
  authoring site   http://localhost:8080
  public site      http://localhost:4000
```

`bun dev:author` serves only the authoring site, and `bun dev:public` only the public site. Each
prints its own address when it is ready.

The public preview rebuilds when a template changes: it regenerates the templates, republishes the
site, and serves the new files. The authoring site is a compiled Go binary, so a template change
needs a restart to take effect there.

`bun dev:setup` starts the PostgreSQL service from `compose.yaml`, writes a `.env` file, applies the
migrations, and fills the library with generated work. It is the reset: run it again to start over.

The `.env` file holds `DATABASE_URL`, `VERSE_AUTHORIZATION` and `VERSE_AUTH_SECRET`. The passphrase
and the signing secret are generated, so nothing is written down anywhere and nothing has to be
chosen by hand. **`DATABASE_URL` is always pointed at the local database**, and the previous host is
named when that changes: the file may hold a hosted one, and carrying it forward would mean local
development reads and writes the author's real work.

The `DATABASE_URL` line is **replaced where it is**, not appended. A file that accumulates one copy
per setup run ends up with a hosted database at the top and a local one at the bottom, and which of
them a tool reads depends on a rule the file does not say. One line in the file is one value, and
one value cannot disagree with itself. A second definition of the variable is dropped when the file
is rewritten, so a leftover copy of a hosted connection string does not survive there either.

The passphrase and the signing secret are **kept** if the file already has them. A passphrase the
developer has typed into a running session is not theirs to have changed under them, and rotating
the signing secret would invalidate the session it signs.

The library the seed writes is generated, not copied, so no real work is reproduced by accident. It
is deterministic for a given seed, so a reseed reproduces the same library rather than a different one
each time. It holds about sixty works spread over the last ninety days, with a run of recent days
so the dashboard's streak reads as more than one, ten of them published so the public site has an
index and an archive, six soft-deleted so the recycle surface has something in it, and a few with
earlier revisions so the history and restore paths have something to act on.

```bash
go run ./cmd/seed -reset          # empty the library and fill it again
go run ./cmd/seed -count 200       # a larger library
go run ./cmd/seed -seed 7          # a different library of the same shape
```

The file is gitignored, and Bun loads it into the environment of anything it runs, which is how
`bun dev` passes those values to the server. **The application does not read a `.env` file.** There
is no dotenv loader in it, and there never was; what reads the file is Bun, before the process
starts.

A variable already present in the environment wins over the file, so a one-off override needs no
editing:

```bash
DATABASE_URL="postgres://verse:verse@localhost:5432/verse?sslmode=disable" bun dev
```

**The dev servers refuse a database that is not on this machine.** They write: the author server
saves work, and the public build publishes it. Pointed at a hosted database, that is somebody's real
work. `VERSE_DEV_ALLOW_REMOTE=1` overrides the check, for the rare case of deliberately developing
against a hosted database. The seed command carries the same rule under `VERSE_SEED_ALLOW_REMOTE`.

`bun dev` builds the Tailwind stylesheet before starting the server. The stylesheet is a build
artifact and is not committed, so without it the application renders unstyled; the step is part of
the command rather than something to remember.

`bun dev:public` publishes the public site into `dist/` and serves it, so the reading surface can
be inspected at <http://localhost:4000> without a deploy. It serves the same contract the
deployment does: `/`, `/archive/` and `/share/` resolve to their `index.html`, and an unknown path
answers with the built `404.html`. The public build reads the published set from the database, so it
needs a `DATABASE_URL` that has published work in it.

The equivalent manual steps, for a checkout where the scripts are inconvenient:

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

---

## Environment variables

### Required

| Variable              | Purpose                                                                                                                                 |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `DATABASE_URL`        | PostgreSQL connection string for the serving credential. The application will not start without a reachable database                    |
| `VERSE_AUTHORIZATION` | The authoring passphrase, compared against the submitted value in constant time. **Minimum 16 characters**                              |
| `VERSE_AUTH_SECRET`   | Key used to sign session and CSRF tokens. **Minimum 32 characters.** Treat it as a secret: changing it invalidates every active session |

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

| Variable                         | Default                                                                                                                                                                                                                                                                               | Purpose                                                                                 |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| `MIGRATION_DATABASE_URL`         | Connection string for the startup migration, which is then discarded. Unset, the migration falls back to `DATABASE_URL` and the boot log says so. Set it to drop DDL rights from the serving credential — see [Dropping DDL rights](#dropping-ddl-rights-from-the-serving-credential) |
| `PORT`                           | `8080`                                                                                                                                                                                                                                                                                | Listen port. Platforms such as Render inject this                                       |
| `DB_MAX_CONNS`                   | `5`                                                                                                                                                                                                                                                                                   | Maximum pooled connections                                                              |
| `DB_MIN_CONNS`                   | `1`                                                                                                                                                                                                                                                                                   | Minimum pooled connections                                                              |
| `DB_MAX_CONN_LIFETIME`           | `1h`                                                                                                                                                                                                                                                                                  | Maximum connection lifetime                                                             |
| `DB_MAX_CONN_IDLE`               | `5m`                                                                                                                                                                                                                                                                                  | Idle time before a connection is closed                                                 |
| `VERSE_STATIC_DIR`               | `static`                                                                                                                                                                                                                                                                              | Asset directory, resolved relative to the working directory                             |
| `TRUSTED_CLIENT_IP_HEADER`       | _(unset)_                                                                                                                                                                                                                                                                             | Request header to take the caller's address from, for the login rate limiter. See below |
| `SERVER_READ_HEADER_TIMEOUT_SEC` | `10`                                                                                                                                                                                                                                                                                  |                                                                                         |
| `SERVER_READ_TIMEOUT_SEC`        | `30`                                                                                                                                                                                                                                                                                  |                                                                                         |
| `SERVER_WRITE_TIMEOUT_SEC`       | `60`                                                                                                                                                                                                                                                                                  |                                                                                         |
| `SERVER_IDLE_TIMEOUT_SEC`        | `120`                                                                                                                                                                                                                                                                                 |                                                                                         |
| `SERVER_SHUTDOWN_TIMEOUT_SEC`    | `15`                                                                                                                                                                                                                                                                                  | Grace period for in-flight requests on `SIGTERM`                                        |
| `SERVER_MAX_HEADER_BYTES`        | `1048576`                                                                                                                                                                                                                                                                             |                                                                                         |
| `SERVER_MAX_BODY_BYTES`          | `1048576`                                                                                                                                                                                                                                                                             | Largest accepted request body                                                           |

The pool defaults are tuned for a managed connection-limited database such as Neon. They are
deliberately conservative and rarely need changing.

### `TRUSTED_CLIENT_IP_HEADER`

The login rate limiter buckets callers by address, so it has to know the caller's real address. It
cannot read that from `X-Forwarded-For` on a directly reachable service, because a client can send
that header with any value it likes and thereby evade its own limit, or aim it at somebody else.

Naming the header makes that an explicit statement about the deployment rather than an inference
from an unrelated variable. Set it to the header your front proxy overwrites:

| Front            | Value                                                                       |
| ---------------- | --------------------------------------------------------------------------- |
| Cloudflare       | `CF-Connecting-IP` - the edge sets it and strips any client-supplied value  |
| Render only      | `X-Forwarded-For` - the rightmost entry is the one the nearest hop recorded |
| Direct, no proxy | _(unset)_ - only `RemoteAddr` is trustworthy, and that is the default       |

A value that is a comma-separated chain is reduced to its rightmost entry, so one variable serves
both a single-address header and a chain. A value that is not a valid address is ignored and the
socket address is used instead, so a misconfigured proxy produces one odd bucket rather than an
arbitrary string used as a key.

**Unset is the failure mode to watch for.** With no proxy declared, every caller through a proxy
shares the socket address, which means one shared rate-limit bucket: anyone can exhaust the author's
own budget by guessing wrong a few times. That failure is silent, which is the problem - nothing logs
a disagreement. If the service is ever placed behind something new, set this in the same deploy.

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

### `007_published_at.sql` and what it did to your library

**Nothing, and that is the point.** The migration added a nullable `published_at` column to `poems` and
left every existing row `NULL`. Your library is byte-for-byte what it was: the same works, the same
timestamps, the same soft-deleted rows, and a full `verse-export` produces an identical file. If you
take a backup after upgrading, the only difference is the backup's own `exported_at`.

There is deliberately no `DEFAULT` on the column. A `DEFAULT now()` would have published your entire
library — every draft, every unfinished piece, anything you deleted and kept — the moment the next
deploy ran, with no way to tell from the outside. **Every work is a draft until you publish it, and
nothing in this repository publishes anything yet.**

|                          |                                                                                                                                                     |
| ------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| What a draft is          | A work with `published_at` set to `NULL`. Nothing else.                                                                                             |
| What you can see         | Everything, as before. The library, the search box, the dashboard and `/poem/<id>` are unchanged — they filter on `deleted_at`, not on publication. |
| What a publisher can see | Nothing yet. There is no public site in this release.                                                                                               |
| What to run              | Nothing. There is no publish command.                                                                                                               |

**If you want a backup anyway**, it is the command you already have:

```bash
go run ./cmd/verse-export --out verse-backup.json --include-deleted
```

`--include-deleted` matters: a complete archival copy includes the soft-deleted works, which the default
excludes. This is the copy that matters if the column ever needed removing.

### What the runner guarantees

- **Each file runs in its own transaction, and its bookkeeping row is written in that same
  transaction.** A migration that fails partway leaves neither partial schema nor a false record of
  success, and the next run resumes from that file.
- **Already-applied files are verified and skipped**, not re-applied. Each record stores a SHA-256 of
  the file.
- **Editing an applied migration is an error.** The recorded checksum will not match, and the runner
  stops rather than applying one version of a file to a database that already has another. Write a
  new migration instead.
- **A consequence worth knowing: applied migration files are frozen, comments included.** The checksum
  covers the whole file, so fixing a typo or a stale path inside one will stop the next boot with
  "migration N was modified after it was applied". This is not theoretical — it happened during
  v0.4.11, when a reference to this file was updated inside `006_timestamptz.sql` and the very next
  test run refused to migrate. The fix is to leave it: the file records what was applied at the time
  it was applied, and a comment in it describes the world as it was. `006` still refers to this
  document as `docs/RUNNING.md`, from before it moved to the repository root.
- **Deleting or renaming an applied migration is an error.** Renaming looks exactly like deleting one
  and adding a new one, and the database has already absorbed the old one.

- **A migration that adds a column with a `DEFAULT` is rewritten, not merely slow.** `ADD COLUMN ...
DEFAULT <value>` that is not a constant takes `ACCESS EXCLUSIVE` for the whole rewrite, on a live
  table the service is querying. v0.4.7's `006_timestamptz.sql` shows the shape this project uses
  instead: add the column, backfill in bounded batches, then add the constraint.
- **A file that is blank after trimming is skipped and never recorded.** A file containing only
  comments is a valid no-op and is recorded like any other migration.
- **A run is bounded, so a blocked migration fails instead of hanging.** An `ALTER TABLE` needs
  `ACCESS EXCLUSIVE` on the table it rewrites, and the deploy is zero-downtime, so the outgoing
  instance is still serving — and still querying — while the incoming one migrates. The run therefore
  sets `lock_timeout` (5s) and `statement_timeout` (60s) on its own connection, and the caller bounds
  the whole run at 5 minutes. A migration that cannot get what it needs in that time fails, naming the
  file, and the next deploy retries against a quieter database. Both server-side settings are cleared
  before the connection returns to the pool, so they never bound an ordinary request.

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

### Before `006_timestamptz.sql`: one query worth running

`006_timestamptz.sql` rewrites `poems.created_at`, `poems.deleted_at` and `poem_versions.recorded_at`
as `timestamptz`. It carries an explicit `AT TIME ZONE 'UTC'` on each column, so the result is the
same whatever time zone the database server is configured for. That is safe for a database that has
always run in UTC, which this one has. If a database's history is _not_ in UTC — a restored dump, a
branch, an instance moved somewhere else — the zone was discarded on the way in, so the stored values
look exactly like UTC wall clocks and no migration can detect the difference.

Worth running before deploying rather than after wondering:

```sql
SELECT count(*) FILTER (WHERE created_at IS NULL) AS null_created_at,
       min(created_at)                       AS earliest,
       max(created_at)                       AS latest
FROM poems;
```

A non-zero `null_created_at` is harmless and expected to be carried through unchanged. The `earliest`
and `latest` values are the point: if they line up with when the work was actually written, the
assumption holds. If they are shifted by a whole zone offset, correct the rows **before** converting
them rather than after. Take the export first either way.

### Why migrations run at startup, and not as a deploy step

The ideal shape is a step between the build and the deploy: the serving credential would then need no
DDL rights at all.

Render provides that hook only for "paid web services, private services, and background workers". A
pre-deploy command needs a **paid compute plan**, not merely a paid workspace, and this service runs
on a free instance. The setting would be accepted by `render.yaml` and then silently never run — the
worst possible failure mode for the step everything else depends on. An earlier draft of this change
had exactly that, and it was removed.

So the service applies its own migrations at startup. One thing follows, and it is worth stating
plainly: **the schema can never be ahead of the code.** Both come from the same binary, and the `.sql`
files are embedded in it.

That is a reason for the migration to run here. It is **not** a reason for the serving credential to
hold DDL rights, and this section previously said so — it concluded that the runtime credential must
keep them "for the life of the process". That conclusion was wrong, and it went unexamined for eleven
releases because the two facts were treated as one. The migration needs `CREATE` and `ALTER`; nothing
that answers a request needs either.

### Dropping DDL rights from the serving credential

This is available on a **free plan**. It does not need `preDeployCommand`, because it is not a deploy
step — the migration still runs at boot, it just runs over a _different connection_:

| Variable                 | Used for                                   | Rights needed                                                        |
| ------------------------ | ------------------------------------------ | -------------------------------------------------------------------- |
| `MIGRATION_DATABASE_URL` | the startup migration, then discarded      | `CREATE`, `ALTER`, ownership of the tables                           |
| `DATABASE_URL`           | every request, for the life of the process | `SELECT`, `INSERT`, `UPDATE` — and `DELETE` on `login_attempts` only |

The migration pool is opened, used, and closed before the first request is served, so the privileged
credential is not held once the service is up.

**`MIGRATION_DATABASE_URL` is optional.** Unset, the migration falls back to `DATABASE_URL` — the
behavior of every release before this one, and the only arrangement that works against a single-role
database such as the one in `compose.yaml`. While it is unset the boot log says so on every start, so
the weaker arrangement is never silent:

```
MIGRATION_DATABASE_URL is not set; migrating with DATABASE_URL, which means the serving
credential still holds DDL rights.
```

A service that refused to start because an optional variable was absent would be a worse outcome than
a service that still holds DDL rights, so the fallback exists on purpose. The split is opt-in and
self-announcing rather than a new requirement.

#### Setting it up

1. Run [`db/roles.sql`](db/roles.sql) once, as an owner or superuser, **connected to the
   application database**:

   ```bash
   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -v app_password='choose-something-long' -f db/roles.sql
   ```

   `-v app_password=...` is required, and the script refuses to run without it rather than creating a
   role with an empty password. It is safe to re-run: it creates the roles if they are absent and
   resets the password if they are not, so it doubles as the rotation procedure.

   **`ON_ERROR_STOP=1` is part of the command, not decoration.** The script signals a missing password
   by raising an exception, and that only produces a non-zero exit status when this flag is set —
   without it, `psql` prints the error, carries on, and exits `0`. (The obvious alternative does not
   work either: psql's `\quit` takes no argument, so `\quit 1` prints a warning and still exits `0`.)
   The flag is also what makes a genuine failure in the middle of the script stop it rather than
   continuing with half the grants applied.

   It is deliberately **not** a migration: migrations are applied at every boot by the service, and
   this needs a credential the service should not hold.

   This exact command is what the `Least privilege` CI job runs on every pull request, so the file is
   exercised rather than merely described.

2. Add `ALTER DEFAULT PRIVILEGES FOR ROLE <your migration role> ... GRANT ... TO verse_runtime` as
   described in that file, or the first future migration will create a table the service cannot read.
3. On Render, set `MIGRATION_DATABASE_URL` to the current credential — the same value `DATABASE_URL`
   has today — and change `DATABASE_URL` to the `verse_app` one.

**Order matters only in one direction.** Do not revoke the owner credential from `DATABASE_URL` before
`MIGRATION_DATABASE_URL` is set, or the service has nothing to migrate with and will not start.

#### What the serving credential cannot do

This is checked on every pull request by the `Least privilege` CI job, which boots the service on a
restricted role and asserts fourteen properties. It cannot `CREATE`, `ALTER` or `DROP`; it cannot
`DELETE` or `TRUNCATE` a work; it cannot modify a retained revision; and **it cannot read
`schema_migrations`** — the boot log reports which migrations ran, but the serving process cannot
query the table that records it.

Two of those are worth a moment:

- **No `DELETE` on `poems`.** Every delete in this application is a soft delete written as an `UPDATE`
  of `deleted_at`, which is why that column exists. The one copy of the work that must never be lost
  cannot be destroyed through the serving credential.
- **No `UPDATE` on `poem_versions`.** A retained revision is immutable by design; restoring one is an
  ordinary edit, which records the displaced text as a new version in turn.

The full suite still runs as the owner, and deliberately is _not_ run against the restricted role: it
truncates, and `TRUNCATE` is not in the grant set. Granting it would make the job claim a
least-privilege role it does not have.

#### A note on what this does not fix

Nothing here reduces the blast radius of a compromised **migration** credential, which still owns the
tables. The improvement is that the credential which is reachable by every request in the world no
longer does.

### If this service moves to a paid compute plan

The better arrangement becomes available, and nothing else changes — the runner, the bookkeeping and
the tests are identical:

1. Add `go build -tags netgo -ldflags="-s -w" -o migrate ./cmd/migrate` to `buildCommand`.
2. Add `preDeployCommand: ./migrate` to `render.yaml`.

**Step 2 is now optional rather than a prerequisite.** The list above used to carry a third step —
"drop DDL rights from the credential the service runs with" — gated on step 2 being in place first. That
gate was the wrong conclusion drawn from a correct observation, and it cost the DDL rights eleven
releases: see the section above. The rights are dropped by setting `MIGRATION_DATABASE_URL`, which works
on a free plan and does not involve a deploy hook at all.

What the paid plan buys on top of that is narrower: the migration stops running at boot, so the
privileged credential is not used at all during a normal start, and a slow or lock-contending
migration cannot delay one. Worth having, and not worth waiting for.

The container image already ships `/app/migrate` for applying a migration by hand without deploying:

```bash
docker run --rm -e DATABASE_URL="$DATABASE_URL" --entrypoint /app/migrate verse:ci
```

### Concurrency

Migrations are serialized across processes by a Postgres advisory lock, so two instances starting at
once cannot both apply the same file. The applied set is read _after_ the lock is taken — reading it
first would let both processes conclude the same migration was pending. The wait is bounded at 30
seconds and reports a clear error rather than appearing to hang.

This is insurance rather than a current need: `WEB_CONCURRENCY` is 1 and there is a single instance.
It matters because a free instance is recycled periodically, so every new process runs the migration
step, and it would matter immediately on any scale-up.

### Login rate limiting

Wrong passphrases are counted per caller and refused once they add up. Without it, the passphrase is
a shared secret on an endpoint reachable from the internet with no other barrier.

|           |                                                                       |
| --------- | --------------------------------------------------------------------- |
| Threshold | 5 failures                                                            |
| Window    | 15 minutes, after which failures are forgotten                        |
| Backoff   | 1 minute at the threshold, doubling per further failure, capped at 15 |
| Response  | `429` with `Retry-After`, and a body with no numbers in it            |
| Key       | HMAC-SHA256 of the client address, under `VERSE_AUTH_SECRET`          |

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
room for up to fifteen minutes. That is accepted for now, and the honest reason is that **there is no
outer gate**. This section previously named "the IP allowlist below" as the mitigation; no such
control exists in this repository, and the section below this one describes Cloudflare Access, which
is an identity layer rather than an address range. Access is planned for v0.6.0 and is the control
that will actually narrow who can reach the service — see issue #72, which also covers the
`onrender.com` origin bypass that would otherwise make it decorative.

Until then the rate limiter is the only defence, and it is an inner one. If a second proxy is ever
placed in front, every caller through it shares a bucket; the right-side rule cannot be forged, so the
answer is to narrow who can reach the service rather than to widen what is trusted.

### When a login is refused

A refused login answers `403` with the body `forbidden`, whatever the reason, because a client that
cannot produce a valid token has no business learning which of its mistakes it made. The reason goes
to the log instead, and each one reads differently:

| Log line                                                            | Means                                                                                                                                                                                                              |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `no synchroniser cookie was sent; request arrived over plain HTTP…` | **A deployment fault, not an attack.** A browser refuses to store a `Secure` cookie on a plain-HTTP page, so the form renders and the submission then cannot be correlated with it. The message says so explicitly |
| `the form carried no synchroniser field`                            | The template did not render a token. Ours, not the caller's                                                                                                                                                        |
| `the synchroniser cookie did not match the form`                    | A token was sent and was wrong                                                                                                                                                                                     |
| `cross-origin submission`                                           | The attack this defence exists for                                                                                                                                                                                 |
| `rate limited, retry in Ns`                                         | Too many failures; see above                                                                                                                                                                                       |

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

| What happened           | How to undo it                                                                                                           |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| A bad edit overwrote it | Open the work, then **History**. Every superseded draft is retained, newest first, and restoring one is itself undoable. |
| It was deleted          | **Recycle**, reachable from the library. Soft-deleted work is listed there and can be restored; it keeps its history.    |
| The database was lost   | `verse-export` from before the incident, restored with `psql`.                                                           |

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

Since v0.4.9 that exercise asserts the image _renders_, not merely that it starts. The job requests
`/static/js/htmx.min.js`, `/static/js/navigation.js` and `/static/css/output.css` from the running
container and requires a 200 for each, then greps the served stylesheet for a generated utility class.
Earlier revisions checked only `/health` and that `/library` refuses an anonymous caller, and an image
with no JavaScript and no Tailwind utilities passed both: `/health` answers from the process, and
`/library` is refused by the router before any handler runs. That gap is why the image spent its
existence shipping a 4,165-byte stylesheet with no utility classes in it — see R16 in
`.opencode/RISK_REGISTER.md`.

If you add a static asset, add it to the same assertions. The allowlist in
`internal/server/router.go` is the other place that needs to hear about it.

### Building and deploying the public site

The public archive is a static build, not a second web service. It reads the published set, renders
every page in memory, validates the result, and writes the output directory. Readers never hold a
database credential, and the deployed files do not depend on Render or Neon at request time.

**The published set travels as a file.** The authoring environment holds the database, so it is the
only place the published set can be read from. It writes that set out as an export and pushes it; the
deployment environment fetches the file and builds the site from it.

That arrangement is not a preference. A build that reached into the database would need a database
credential in the build environment, and a credential in a build command is a credential in a build
log -- which is how the serving password was exposed once already. The build environment now holds
no database credential at all, and a deploy does not fail because the database is unreachable.

```bash
# In the authoring environment: write the published set and push it.
go run ./cmd/verse-publish --out verse-public.json

# In the deployment environment: build the site from the artifact.
go run ./cmd/publish -from verse-public.json -out dist -base-url https://your-public-origin.example
```

The two produce the same bytes. `TestTheTwoPathsRenderIdenticalBytes` holds them to it, byte for
byte across every output, so the file path is not a second publisher with its own idea of what is
public.

The export is the works a reader can see and nothing else: `cmd/verse-export --public` and
`cmd/verse-publish` both apply the publisher's own filter, so a draft cannot reach the artifact even
by accident. The artifact carries a digest of that published set, and every page records it, so the
live site is the record of what was last deployed and a build can tell whether anything a reader can
see has changed.

The deployment is a GitHub Actions workflow, `.github/workflows/publish.yml`. It runs every fifteen
minutes, on a dispatch from the authoring side, or by hand. It fetches the artifact, compares the
digest with the live site's, and deploys only when they differ.

```bash
# What is published right now. Writes nothing.
go run ./cmd/publish -list

# Build into dist/. Use the read-only serving credential, not the migration credential.
DATABASE_URL="postgres://verse_app:...@host/db?sslmode=require" \
  go run ./cmd/publish -out dist -base-url https://your-public-origin.example
```

The listing was the dry run once -- a way to see what publishing would expose, before anything in
this repository wrote to `published_at`. Publication is an explicit act now, so the useful question
is no longer "what would this expose" but "what is out there", which is what an operator needs
when the site and the library disagree. The name moved with the question.

`-base-url` (or `PUBLIC_BASE_URL`) is required for a real deployment: `sitemap.xml` and `feed.xml`
use absolute URLs. The default is the reserved placeholder `https://verse.example`, which is
deliberately not a real host. The output is a pure function of the published set and the configured
origin; there is no build timestamp, so two builds from the same input are byte-identical.

### The reader's site, and where its navigation lives

The public site is three screens: a front page, an archive, and a share surface. Its navigation is
**on the screen**, in a rail on each side of the content, and the two rails point at the reader's
neighbours in a ring: home, then the archive, then share, and home again. Every screen is one click
from both of the others.

The brand pill in the corner is the one thing that opens a menu, and that menu points **outward**:
at the author's other portfolio sites. It is a `<details>` element, so it toggles on click, takes
focus, and opens on Enter or Space, and the public build ships no JavaScript at all. The site's own
pages are never in that menu — they are in the rails, where they can be seen.

The portfolio links are configuration, not markup:

```bash
go run ./cmd/publish -out dist \
  -base-url https://your-public-origin.example \
  -links "Studio=https://studio.example,Notes=https://notes.example"
```

`PUBLIC_LINKS` is read when the flag is absent. The list is comma-separated `Label=URL` pairs; a
label may contain spaces and a URL may not contain a comma. A link with no label, no URL, or a URL
that is not an `http` or `https` address stops the build rather than producing an empty menu entry.
With no links configured the pill is a plain link home, because a button that opens nothing is
worse than a link.

The share surface shows the **address** of each work rather than only linking to it. The address is
set in a monospace face and marked `user-select: all`, so one click selects the whole of it and the
reader copies it with the keystroke they already know. That is a copy button with no script in it.

The build refuses to write if validation fails. Validation checks that the required files exist, that
no authoring route, asset, htmx attribute, script element, or secret name reached the output, and
that every internal link resolves. It runs before the previous `dist/` is replaced, so a failed build
leaves the last good site in place.

Work pages use `/w/<uuid>/` for now. The identifier is stable and is not derived from the text, so
editing a work cannot change its address. A future `works`/slug migration is additive: existing
URLs must keep resolving, either directly or through a redirect. `search.json` carries only an id, a
path, a title, a date, and a bounded excerpt; full body text is not published twice.

**The archive lists a title and an excerpt.** The excerpt is the same bounded summary the search
index and the work page's meta description carry, so nothing is published that was not already. It
is there so that a reader scanning the archive can see what a work is about without opening it, and
so that a search match on a phrase inside a work is explicable: the matched phrase is marked in the
excerpt.

The excerpt is rendered by the server, not by the script. The script adds the search box and the
highlight; the list itself is the complete list of published work whether the script loads or not,
and it says the same thing either way.

Until the domain decision (D7) lands, `robots.txt` disallows every crawler and every page carries
`<meta name="robots" content="noindex">`. Deploy on a platform subdomain, not a custom domain.

The public site deploys to Cloudflare Workers Static Assets as the Worker `verse`. `wrangler.jsonc`
is committed, so the deploy never runs Wrangler's interactive setup. The committed `name` must stay
identical to the Worker name in the Cloudflare dashboard.

### The publish pipeline

The pipeline has three parts, and each of them holds exactly one credential:

| Part                | Holds                                      | Does                              |
| ------------------- | ------------------------------------------ | --------------------------------- |
| The authoring app   | a KV-write token, a GitHub dispatch token  | stores the set, asks for a deploy |
| The deploy workflow | a KV-read token, a Cloudflare deploy token | fetches the set, builds, deploys  |
| The build           | nothing                                    | renders the set                   |

The published set travels as a file. The authoring environment holds the database, so it is the
only place the set can be read from; it writes the set out, stores it, and asks for a deploy. The
workflow fetches the file and builds from it.

That is what keeps the database out of the build environment, and the database credential out of
every build log.

**The store is a Workers KV namespace.** R2 is the more natural fit for a file, but R2 speaks the
S3 API and every S3 request is signed with SigV4. The SDK that signs it correctly adds twelve
modules to a module graph that had nineteen lines; the KV REST API needs a bearer token and one
HTTP call, and adds nothing. The artifact is a few kilobytes against KV's twenty-five-megabyte
limit.

**The deploy is requested by a dispatch.** The workflow runs every fifteen minutes, on a
`repository_dispatch` from the authoring side, or by hand. Publishing a work dispatches it, so a
publication reaches the site in seconds rather than in a quarter of an hour; the schedule is the
safety net for a dispatch that was missed.

The dispatch carries the digest of the published set it was dispatched for. KV is eventually
consistent -- a value written a moment ago may not be readable from everywhere for up to a minute --
so the workflow waits for the artifact it was dispatched for rather than building the one that
happened to be there.

### What the operator sets

On the **authoring service** (Render):

| Variable                     | What it is                                                |
| ---------------------------- | --------------------------------------------------------- |
| `CLOUDFLARE_ACCOUNT_ID`      | the account the namespace is in                           |
| `CLOUDFLARE_KV_NAMESPACE_ID` | the namespace the published set is stored in              |
| `CLOUDFLARE_KV_WRITE_TOKEN`  | a token that can write one key in that namespace          |
| `GITHUB_REPOSITORY`          | `owner/name` of the repository the deploy is requested in |
| `GITHUB_DISPATCH_TOKEN`      | a token that can dispatch a workflow in that repository   |

The GitHub token needs `Contents: write` on that one repository. That is the permission the
dispatch endpoint requires, and it is more than the workflow itself needs -- the workflow reads the
repository and nothing else. A token that can only read is not enough: the dispatch endpoint has no
narrower permission to give.

On the **repository** (GitHub Actions):

| Secret                       | What it is                                      |
| ---------------------------- | ----------------------------------------------- |
| `CLOUDFLARE_ACCOUNT_ID`      | the account the namespace and the Worker are in |
| `CLOUDFLARE_KV_NAMESPACE_ID` | the namespace the published set is stored in    |
| `CLOUDFLARE_KV_READ_TOKEN`   | a token that can read one key in that namespace |
| `CLOUDFLARE_API_TOKEN`       | a token that can deploy the Worker              |

| Variable              | What it is                                            |
| --------------------- | ----------------------------------------------------- |
| `PUBLIC_SITE_URL`     | the site's own origin, which the sitemap and feed use |
| `PUBLIC_ARTIFACT_KEY` | the key the published set is stored under             |

The two Cloudflare tokens are separate because the two sides are separate: the one that writes can
do nothing but write, and the one that reads can do nothing but read. A single token would mean the
deployment could overwrite the artifact it is about to build from.

The worker itself still deploys through `bunx wrangler deploy`, and `wrangler.jsonc` is committed so
the deploy never runs Wrangler's interactive setup.

Preview builds must use the Preview command (`bunx wrangler preview`), not the deploy command.
Previews run against a branch-specific internal script, so `wrangler deploy` reports a misleading
name mismatch against production.

A manual deploy uses the same committed configuration:

```bash
go run ./cmd/publish -from verse-public.json -out dist -base-url https://verse.divijganjoo2003.workers.dev
bunx wrangler deploy
```

Keep the previous `dist/` (or the previous deployment) until the new one has been checked. Rolling
back is redeploying the last known-good build, not a database operation.

To publish by hand -- which is what to reach for when the automatic path did not run:

```bash
go run ./cmd/verse-publish --out verse-public.json
```

It writes the published set, stores it, and asks for a deploy.

### Checking the push path without Cloudflare or GitHub

Two environment variables override the addresses the push path talks to:

| Variable                    | What it is                     |
| --------------------------- | ------------------------------ |
| `VERSE_CLOUDFLARE_API_BASE` | the root of the Cloudflare API |
| `VERSE_GITHUB_API_BASE`     | the root of the GitHub API     |

They exist so that the whole path can be exercised against a local stand-in, which is how it is
verified. **They must point at the local machine.** An override is a redirection: it says where the
published set is written and where the deploy is requested, so an unrestricted one would be a way to
point the artifact at somebody else's host and to carry the dispatch token to it. A non-loopback
address is refused rather than honored.

Unset, they mean the real APIs, which is the normal arrangement.

### The database and the service belong in the same region

The service runs in Oregon. The database runs wherever its project was created, and a project cannot
change region after it is created -- moving it means creating a new one and migrating.

The two are named independently, so they can end up in different places, and the cost of that is not
obvious from the configuration. A service in Oregon talking to a database in Singapore pays roughly a
180-millisecond round trip on every query, on a page that issues several, and nothing about the
deployment would say so.

The boot log answers it:

```
verse starting, version 9ff1ff9
database: ep-whatever-pooler.us-west-2.aws.neon.tech (from DATABASE_URL)
```

The host is logged and nothing else; the connection string carries a password, and a password belongs
in neither a log nor an incident report. The region is in the host name, so one line answers "where
is my database".

`render.yaml` pins the service's region explicitly rather than leaving it to the platform's default,
because the default is not visible anywhere in this repository -- and the dashboard and this file
have diverged before.

### Publishing a work

A work reaches the public site when the author publishes it. The work's own screen carries the
control, and it says which state the work is in, because the difference between published and
draft is the difference between a reader seeing a work and not.

Publication is an explicit act. Nothing publishes anything by accident: the column has no default,
the migration that added it left every existing row null, and the publisher's read is the only place
the column is consulted.

A work in the recycle bin cannot be published. The publisher's filter excludes it, so publishing one
would set a state the site would never show and the author would never be able to undo; the action
refuses and says why.

### The TypeScript build

The browser code is TypeScript, compiled by Bun and committed. The compiled files are in the
repository because `go build` must not depend on a code-generation step, and because the static
allowlist test asserts that `/static/js/editor.js` is served -- a file that is only produced by a
build step is not there on a fresh checkout.

```bash
bun scripts/build_js.ts             # rebuild the bundles
bun scripts/build_js.ts --check     # fail if they are stale
bun check                           # check the types
```

Bun's bundler strips types and never checks them, so the type check is a separate step and it is a
step and not a comment. It found twenty-one issues in the JavaScript it replaced, including one
unguarded `EventTarget` in the editor's backdrop handler.

`bun check` is Bun's own type checker, not a script in `package.json`. It reads the repository's
`tsconfig.json` and reports the same errors `tsc --noEmit` does, down to the codes the strict
settings produce. Using it means the repository does not carry the TypeScript compiler as a
dependency for the sake of checking seven files, and the type checker is always the same version
as the bundler that compiles the sources. The `qa` script runs it.

`bun run dev:author` rebuilds the stylesheet and the bundles when they change. It did not until
v0.5.0, and the reason was the port: the bundles used to be hand-written, so editing one was
picked up on the next reload, and they are compiled output now, so an edit to `web/ts/editor.ts`
left `static/js/editor.js` stale until the build was run by hand.

Bun does not downlevel. A `??` written in a source file is a `??` in the output, whatever the
tsconfig says, so the transpile floor is held by the `lib` setting -- an ES2022 library call is a
type error -- and by the rule that the source stays inside ES2020.

The public site's one script is the archive's search box. It is inserted by the script rather than
being in the markup, so a reader with no script sees no input at all rather than an input that
cannot search, and the archive page is the complete list of published work either way.

### `render.yaml` is the source of truth; the dashboard must mirror it

The two have already diverged once. The dashboard's build command was a copy of an older revision of
`render.yaml`, so Render was building with an unlocked dependency install where the repository
specified `--frozen-lockfile` — and nothing noticed, because nothing compared them.

When changing how the service is built, change `render.yaml` **and** the dashboard setting, in the
same commit. CI checks the repository's Bun pins against each other; it cannot see the Render or
Workers Builds dashboards.

### Toolchain: Bun floats `latest`, Node is pinned on Workers Builds, Go cannot be pinned

|      | CI and `Dockerfile`                                                 | Render                                       | Workers Builds                                        |
| ---- | ------------------------------------------------------------------- | -------------------------------------------- | ----------------------------------------------------- |
| Bun  | `latest`, asserted equal across CI, `Dockerfile`, and `render.yaml` | `latest`, via `BUN_VERSION` in `render.yaml` | `latest`, via `BUN_VERSION`                           |
| Node | —                                                                   | —                                            | `24`, via the committed `.node-version`               |
| Go   | `1.27.x` from `go.mod`, asserted equal to the `Dockerfile`          | **latest stable 1.x** — not pinnable         | build image default (`1.27.x`); `go.mod` sets a floor |

Render's default Bun depends on when the service was created, and this one defaulted to `1.3.4` while
the repository was on `1.3.5` and then `1.4.2`. Production was therefore building the stylesheet with
a package manager version no other build path used. `BUN_VERSION: latest` fixes that, and CI now fails
if the three repository paths disagree — including when the policy is _absent_, which was the state
that produced `1.3.4`. The trade-off is deliberate: a build is not reproducible across time, because
all paths resolve `latest` when they run. A literal pin remains accepted by the gate, but every path
must name the same literal; mixing `latest` with a pin is what the gate rejects. Workers Builds uses
the same `latest` policy; its dashboard variable is not visible to CI.

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

This is a change of behavior, and deliberately so. It previously answered `200` without touching the
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
the logs, not a `503`.

A `503` means the check did not succeed within `HealthPingTimeout` (1.5s). That is _not_ the same as
"the database is gone", and the distinction matters when deciding whether to restart. It is reported
when the database is unreachable, and also when it is merely slow — including when the connection
pool is saturated, which the probe cannot tell apart from a slow database from inside the handler. The
log line carries the underlying error, so the cause is always recoverable from there rather than from
the status code.

That is also why the deadline is short and separate from the request timeout. The pool holds five
connections and the platform probes every few seconds, so a probe that inherited the 30s request
timeout could hold a scarce connection for half a minute — long enough to starve the application it
shares the pool with, turning a slow database into a total outage. `TestHealthProbeYieldsWhenThePoolIsExhausted`
drains the pool and asserts the probe still answers in about a second and a half.

### Diagnosing a refusal

A refused request answers `403` with a body of exactly `forbidden`, and that is a contract. A caller
that cannot produce a valid token has no business being told which of its mistakes to correct —
distinguishing the reasons is the entire value of the synchroniser token.

Every response carries `X-Request-Id`, including refusals. `requestIDHeaderMiddleware` in
`internal/server/router.go` honours an inbound `X-Request-Id`, so on Render this is the platform's own
identifier, and the same value appears in the platform's request log. That is what lets a bug report
be turned into a log line.

The value is bounded before anything reads it, by `inboundRequestIDMiddleware`. This document
previously named `middleware.RequestID`, which does not exist: there is no `internal/middleware`
package, and never has been. The empty directory sat untracked in a working tree, which is enough for
a path check to pass and not enough for the project to contain it — the README's project tree made
that mistake in the same release, and `TestReadmeProjectTreeNamesOnlyRealPaths` caught it in CI but
not locally, for the same reason.

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
- **Static assets are revalidated, not cached.** The authoring side serves its script and stylesheet
  from fixed, unhashed URLs — `/static/js/editor.js` and `/static/css/output.css` — so they are sent
  with `Cache-Control: no-cache`. The browser checks with the server on every load and gets a `304`
  when nothing changed, which costs a request and no body.

  This matters more than it looks. A long cache on an unhashed URL means a browser that loaded the
  site before a deploy keeps running the previous release's script, and a fix that has already
  shipped appears to have done nothing. The Focus Mode close control stayed broken for a day after
  its fix was deployed for exactly this reason.

  Long caching would be safe with content-hashed filenames, the way the public site's stylesheet is
  already hashed. That is not how the authoring side is built, so it revalidates instead.

- **Health of the commit graph.** A change is validated when its pull request targets `main`, and
  again when it merges into `main`. Superseded runs are canceled automatically. See
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

| Symptom                                                                      | Cause                                                                                             | Fix                                                                                                                                                                                                            |
| ---------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `refusing to start: authentication is not configured`                        | `VERSE_AUTHORIZATION` or `VERSE_AUTH_SECRET` is unset, or either is under its minimum (16 and 32) | Set both in the environment and restart. The message states both minimums                                                                                                                                      |
| `too many failed attempts` (HTTP 429)                                        | The caller is rate limited; the body is deliberately numberless                                   | Wait for `Retry-After`. If it is you and you have not been guessing, see the rate-limit section above                                                                                                          |
| `database connection failed`                                                 | `DATABASE_URL` unset, unreachable, or the database is asleep                                      | Check the variable; managed databases need a moment to resume                                                                                                                                                  |
| `DATABASE_URL environment variable not set`                                  | The variable is not in the process environment                                                    | Export it, or run `bun dev`, which reads a `.env` file. The application itself never reads one                                                                                                                 |
| `database migration failed: ... another process has held the migration lock` | Two instances are migrating at once, or one died holding the lock                                 | Usually transient; the wait is bounded at 30s. If it persists, check for an instance stuck in `pg_locks`                                                                                                       |
| `database schema is not ready: the poems table does not exist`               | The migration runner reported success but the `poems` table is absent                             | A real inconsistency between the runner and the application; check `schema_migrations` and the migration files                                                                                                 |
| `migration <file> was modified after it was applied`                         | An already-applied `.sql` file was edited                                                         | Restore the original file, or write a new migration. Do not edit history                                                                                                                                       |
| `these migrations are recorded as applied but no longer exist`               | An applied `.sql` file was deleted or renamed                                                     | Restore it. Renaming an applied migration is indistinguishable from deleting it                                                                                                                                |
| `apply migration <file>: ...`                                                | The SQL in a migration failed                                                                     | Nothing was recorded and nothing was left behind; fix the file and re-run                                                                                                                                      |
| Production stylesheet differs from CI                                        | Render's Bun drifted from the repository's                                                        | Check `BUN_VERSION` on the service; CI asserts all three build paths agree                                                                                                                                     |
| Deploy used a stale or different build command                               | The dashboard diverged from `render.yaml`                                                         | Re-sync the dashboard from `render.yaml`; CI cannot detect this                                                                                                                                                |
| A fix is deployed but the browser still behaves as before                    | The browser is running an older script or stylesheet                                              | Reload with the cache bypassed. Static assets are revalidated, so this should only be needed once                                                                                                              |
| Unstyled page                                                                | The stylesheet was not built                                                                      | `bunx @tailwindcss/cli -i ./static/css/input.css -o ./static/css/output.css --minify`                                                                                                                          |
| Blank page or missing navigation in the console                              | The vendored htmx bundle is missing or its checksum changed                                       | Restore `static/js/htmx.min.js`; verify with `cd static/js && sha256sum -c VENDOR.sha256`                                                                                                                      |
| `templ generate` reports `expected operand`                                  | The `@if` builtin is not usable in this project; conditionals must use the `@If(...)` helper      | See `templ/helpers.go` and existing templates for the convention                                                                                                                                               |
| Tests pass but almost nothing ran                                            | No DSN was set, so the database-backed tests skipped silently                                     | Set `VERSE_E2E_DATABASE_URL` and check the skip count in the output                                                                                                                                            |
| Tests fail intermittently with a truncated or missing row                    | Two packages shared one `poems` table                                                             | Should be impossible as of v0.4.3, since each package has its own schema. If it recurs the isolation has been broken: check that `verse_t_tests` and `verse_t_cmdserver` each exist and hold their own `poems` |
