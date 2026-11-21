# `Verse`

> A private daily writing practice.
> Write. Reflect. Return.

Verse is a private, single-author web application for disciplined poetic practice. It is a writing
ritual, not a social platform or productivity dashboard. Published work is read on a separate static
site with an archive, a feed and share-by-link; the private writing space has no recommendations and
no algorithmic interference.

## What it does

- A focused daily editor with version history: every superseded draft is retained, and a restore is
  itself undoable.
- A recycle bin for soft-deleted work, which can always be brought back.
- Streak tracking in UTC days and a monthly activity heatmap.
- `Caelum`, a prompt engine made of ten static, non-imperative prompts — no model, no network.
- Lossless JSON export of the whole library, plus a Markdown rendering for reading.
- Optional sharing: publish a work to a separate static reader's site, mint a private link for one
  person to suggest changes, or mint a public link that records every submission for review.
- Passphrase-protected access. No accounts, no multi-tenancy, no telemetry.

## Get started

```bash
bun run dev:setup     # database, local .env, migrations
bun run dev           # authoring site on :8080, reader's site on :4000
```

Open <http://localhost:8080> and enter the passphrase from `.env`. For manual setup, deployment,
environment variables, backups and troubleshooting, see [RUNNING.md](RUNNING.md).

## Documentation

- [RUNNING.md](RUNNING.md) — operations: setup, configuration, migrations, deployment, recovery.
- [ARCHITECTURE.md](ARCHITECTURE.md) — design, stack, project structure, database schema.
- [SECURITY.md](SECURITY.md) — how to report a security issue.
- [CONTRIBUTING.md](CONTRIBUTING.md) — how to work on Verse.

Verse exists to make daily writing a habit: to create, reflect, and keep a record over time.
It is optimized for depth, not virality.

## License

Private project.
