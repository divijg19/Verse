# Security

Verse is a private, single-author application. There is no user table, no registration, and no
multi-tenancy: whoever holds the passphrase has the whole library.

## Database credentials

The authoring service runs with a serving credential that holds no DDL rights when
`MIGRATION_DATABASE_URL` is configured as a separate owner credential. If that variable is absent,
the service logs the fallback and runs migrations with the serving credential rather than silently
granting itself DDL. See RUNNING.md.

## Public submission links

A Public Link is the only write surface open to anyone who obtains its URL: it is a bearer
capability for one published work. Its entry page shows that work's published text read-only on a
first slide; it carries no other works, drafts, proposals or authoring chrome, and its one first-party
script only wires Copy Text from the text the server rendered (it also reloads on a
back/forward-cache restore). Private links also allow unauthenticated writes, but only after
the holder redeems a per-link URL. Its submissions are appended, never applied automatically, and reviewed by the
author in History or from the Share screen's **View suggestions** overlay. Entry and submission are rate-limited per address; a double-submit CSRF cookie is defence in
depth, not the access control. A private link is a different capability for one person: the editor
draft is keyed by the link, so two holders of the same URL would share one body. Use the explicit
**New** action to create a separate link for each reviewer; the primary "Private Link" button
returns the existing active link. Drafts persist after expiry or revocation but become unreachable through the
guest routes.

The author's Share screen keeps ended rows for the record, sorted last, with no Copy URL. A link
that is merely unrecoverable (an old row without a seed, or a rotated key) is not ended: whoever
holds its URL can still use it, and Rotate is the repair path. Revocation wins over expiry in the
label. Hiding Revoke on ended rows does not delete anything; drafts and submissions remain in History.

### Link token recovery (v0.5.9+)

To make Copy work on an active link after a reload, each row keeps a random, non-secret seed; the
token is recomputed from it under a key derived from `VERSE_AUTH_SECRET`. Only the seed and the
token's SHA-256 are stored, so **a database read or backup alone still yields no usable URL**.
The accepted trade-off is narrower than plaintext: an attacker who obtains both the database and
the application secret can recover active links. Plaintext tokens are never stored.

`token_hash` remains the lookup key and doubles as the integrity check — a derived token is only
shown when its hash matches the stored value. Rotating `VERSE_AUTH_SECRET` therefore leaves existing
URLs working for their holders but removes the author's ability to re-copy them; Share shows no Copy
button rather than handing out a token that would not resolve. Links created before v0.5.9 have no
seed and likewise have no Copy button. No old key is retained, so rotation does not extend the
lifetime of a session-forging credential.

The two Share buttons are idempotent while a recoverable active link exists under the current key:
they return the existing link without changing its token or expiry, so a repeated click never
invalidates a capability already handed out. When no link can be recovered they fail closed: a
private click mints a fresh row, while a public click returns 409 because its single row cannot be
replaced without invalidating a URL already handed out. **New** (private) adds a second conversation;
**Rotate** (public) issues a fresh token on the same row and deliberately retires the old URL. A revoked or unrecoverable public link is never
silently repaired, because the holders of the old URL cannot be told it changed.

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
