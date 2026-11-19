-- Migration: private links and the proposals sent back through them.
--
-- A private link is a capability: whoever holds the URL may open a limited editor for one work and
-- send back a suggested rewrite. It is deliberately NOT a publication. The linked work's row in
-- `poems` is never changed by a guest, and nothing here reaches the reader's static site.
--
-- The raw token is not stored. Only its SHA-256 is, so a read of this table -- a backup, an
-- exported row, a support session -- cannot be used to open somebody's private link. The token is
-- generated with crypto/rand at 256 bits, which makes guessing it infeasible; a hash lookup is
-- therefore enough, and a constant-time comparison against a presented token would be theatre.
--
-- Eight hours, matching the author's own session TTL. A link past that instant is refused, and the
-- guest session it issued expires with it. There is no renewal: a proposal is a short conversation
-- about one draft, not an account.
--
-- Revocation is a nullable instant rather than a deleted row. A revoked link must keep failing for
-- as long as anyone might still hold the URL, and deleting the row would let a later link with the
-- same token (however unlikely) succeed. A timestamp also says when it happened.
--
-- proposals are append-only. A guest can submit more than one, the author can compare them, and
-- applying one is an ordinary edit through UpdatePoem -- which records the displaced text as a
-- version. The proposal itself is kept after applying: the history of a suggestion is as much a part
-- of the work's record as the history of the author's own edits.

CREATE TABLE IF NOT EXISTS private_links (
    id UUID PRIMARY KEY,
    poem_id UUID NOT NULL REFERENCES poems (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ NULL
);

-- The lookup on every guest request is by hash. Unique, because two links must never share a token.
CREATE UNIQUE INDEX IF NOT EXISTS idx_private_links_token_hash
    ON private_links (token_hash);

-- The Share screen lists a work's active links so the author can revoke them.
CREATE INDEX IF NOT EXISTS idx_private_links_poem_active
    ON private_links (poem_id, expires_at DESC)
    WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS work_proposals (
    id UUID PRIMARY KEY,
    poem_id UUID NOT NULL REFERENCES poems (id) ON DELETE CASCADE,
    link_id UUID NOT NULL REFERENCES private_links (id) ON DELETE CASCADE,
    guest_name TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_at TIMESTAMPTZ NULL
);

-- History is read per work, newest first. The link id is kept so the author can see which of several
-- links a suggestion came through.
CREATE INDEX IF NOT EXISTS idx_work_proposals_poem_recent
    ON work_proposals (poem_id, created_at DESC);

-- Privilege: the migration role creates and owns these tables, but the service reads and writes
-- them with verse_runtime, its whole-life credential. Table-level grants are part of this migration
-- because db/roles.sql runs once, before any of these tables exist, and FOR ROLE default privileges
-- are not enabled in this deployment. Without them the Share screen would fail with "permission
-- denied" at the first link creation, which is the worst moment to discover the gap.
--
-- No DELETE on either table. A revoked link and an applied proposal keep their rows so the record
-- survives; the code marks instants instead of removing history.
--
-- The grant is conditional on the role existing. In both the documented setup and the CI
-- least-privilege job, migrations run before db/roles.sql creates verse_runtime, so this condition
-- is normally false. It exists for deployments that pre-create the role, and a GRANT to a missing
-- role would stop the deploy. db/roles.sql carries the unconditional half of the answer: that is
-- where the serving credential gets its access in the normal order. The condition here is checked
-- with to_regrole rather than by permitting an error.
DO $$
BEGIN
    IF to_regrole('verse_runtime') IS NOT NULL THEN
        GRANT SELECT, INSERT, UPDATE ON private_links TO verse_runtime;
        GRANT SELECT, INSERT, UPDATE ON work_proposals TO verse_runtime;
    END IF;
END
$$;

