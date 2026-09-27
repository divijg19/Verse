# Security

Verse is a private, single-author application. There is no user table, no registration, and no
multi-tenancy: whoever holds the passphrase has the whole library.

## Reporting a vulnerability

Report privately through GitHub's security advisory form on this repository
(**Security → Report a vulnerability**). Please do not open a public issue for anything exploitable.

Include what an attacker can do, the version or commit, and the steps to reproduce. A passphrase
bypass, a way to read the library without the passphrase, or a way to write to the database without
it are all in scope and treated as urgent.

## What is already in place

- **A passphrase, not accounts.** `VERSE_AUTHORIZATION` is compared over fixed-length SHA-256
  digests with `ConstantTimeCompare`, so neither the value nor its length leaks through timing.
- **A separate signing secret.** `VERSE_AUTH_SECRET` (32 characters minimum) HMACs the session and
  synchroniser tokens. Rotating it invalidates every session, which is the intended way to force a
  re-login.
- **Synchroniser tokens on every mutation.** Both the login form and every authenticated form carry a
  token that is compared in constant time. This is the actual CSRF control; the `Origin` check is
  defence in depth.
- **Opaque refusals.** A refused request answers `403` with a body of exactly `forbidden`, and no
  header naming the check, unless an operator has deliberately set `VERSE_DEBUG_LOGIN=1`. Telling a
  caller which mistake it made is what the token exists to prevent.
- **Rate limiting on login**, keyed by HMAC of the caller address rather than the address itself.
- **HSTS, CSP, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `nosniff`** on every response.
- **Login logs name the reason** and carry the request ID, so a refusal is diagnosable without being
  disclosed to the caller.

## Known limitations

These are documented rather than hidden, and are tracked as issues where they have one.

- **The runtime database credential holds DDL rights.** It exists because the platform cannot run a
  pre-deploy step on the current compute plan, so migrations run at boot. Issue #59 tracks splitting
  it.
- **Migrations run on every boot**, each instance taking an advisory lock. Correct, but redundant per
  instance.
- **No password recovery.** The passphrase is compared against an environment variable. Losing it
  means the library is unreachable through the application — which is why `verse-export` exists and
  why taking an export is not optional.
- **A single shared secret.** Anyone with both environment variables has everything. There is no
  second factor and no per-request authorisation.

## Handling the work itself

The library is the only copy unless you have exported it. A `verse-export` file is the entire body of
work in plaintext: treat it as the writing itself, keep it out of version control, and give it the
same care as the database.
