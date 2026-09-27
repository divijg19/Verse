# Pull request

## What this changes

<!-- One or two sentences. What is different after this merges? -->

## Why

<!-- The problem, not the solution. What was broken, missing, or risky? -->

Closes #

## How it was verified

<!-- Be specific about what proves this works. "Tests pass" is not an answer on its own. -->

- [ ] `go test ./... -count=1 -race` passes, and DB-backed tests actually ran rather than skipped
      (`VERSE_E2E_DATABASE_URL` and `VERSE_E2E_ALLOW_DESTRUCTIVE` set, database name contains `test`)
- [ ] `golangci-lint run` is clean
- [ ] `templ generate` produces no diff
- [ ] New behaviour has a test that **fails without the change** — if you can show the red, say so

## Things a reviewer should look at

<!-- Where the judgement calls are. Name them rather than making the reviewer find them. -->

- Is this safe if it fails halfway?
- What happens on concurrent requests?
- Does this leak anything into a response, a log, or an error message?

## Risk

- [ ] Touches the schema (a migration was added or an existing one changed)
- [ ] Touches authentication, sessions, or anything that decides who can read or write
- [ ] Touches the deployment (`render.yaml`, the Dockerfile, CI)
- [ ] Can destroy work — anything that overwrites, deletes, or rewrites content

If the last box is ticked, say what the recovery path is and take an export first.
