-- Migration: continuous guest drafts, and one submission per private-link branch.
--
-- A private link is one conversation with one person. Until now the guest editor rendered the work
-- from poems.content on every visit, so a reload lost their work, and every "Send suggestion" wrote
-- another row to work_proposals. This migration adds a single, continuously edited body per link and
-- reshapes proposals so the default send updates one primary submission while an explicit alternative
-- creates a separate branch.
--
-- The draft is keyed by link_id, not by a guest account: there are no guest accounts, and the link
-- is the capability. A link shared with several people therefore shares one draft, which is why the
-- authoring UI documents one link per conversation and offers a fresh link for another reviewer.
--
-- base_hash is SHA-256 of the normalized body the guest was last given. It is metadata, not a second
-- copy of the text: change detection compares against the last send rather than against poems.content,
-- so an author edit in the middle of the guest's window cannot silently declare their work unchanged.
--
-- Dedupe below is destructive. Work proposals are append-only only in the sense that the service
-- never deletes them; the migration owner can, and must, collapse pre-existing duplicates before the
-- unique index can exist. The rule prefers an applied row, because that is the one the author has
-- already acted on, and otherwise the newest row. No table foreign-keys work_proposals.

ALTER TABLE work_proposals
    ADD COLUMN IF NOT EXISTS branch TEXT NOT NULL DEFAULT 'primary'
        CHECK (branch = 'primary' OR branch LIKE 'alt:________-____-____-____-____________'),
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Existing rows have no meaningful "last update": created_at is the closest truth. The column was
-- added with a default of now(), so every existing row already has a value and a WHERE IS NULL
-- backfill would match nothing -- which would pin historical proposals to the top of History.
UPDATE work_proposals SET updated_at = created_at;

WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY link_id
               ORDER BY (applied_at IS NOT NULL) DESC, created_at DESC, id DESC
           ) AS position
    FROM work_proposals
)
DELETE FROM work_proposals p
    USING ranked r
    WHERE p.id = r.id AND r.position > 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_work_proposals_link_branch
    ON work_proposals (link_id, branch);

CREATE TABLE IF NOT EXISTS guest_drafts (
    link_id UUID PRIMARY KEY REFERENCES private_links (id) ON DELETE CASCADE,
    poem_id UUID NOT NULL REFERENCES poems (id) ON DELETE CASCADE,
    guest_name TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    base_hash TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Idempotent grants, conditional for the same two deployment orders as 008. db/roles.sql carries the
-- unconditional half for the normal order in which migrations run before the role is created.
DO $$
BEGIN
    IF to_regrole('verse_runtime') IS NOT NULL THEN
        GRANT SELECT, INSERT, UPDATE ON guest_drafts TO verse_runtime;
    END IF;
END
$$;
