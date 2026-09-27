-- Migration: retain the prior content of every edit, so an overwrite is recoverable.
--
-- A saved work could previously be destroyed by a single bad edit: UpdatePoem overwrote content in
-- place and kept nothing, so a mistaken paste replaced the previous text permanently with no way
-- back. This table is the undo.
--
-- Append-only by convention. Nothing in the application updates or deletes a row here, and a restore
-- is itself an edit, so it lands in this table too and can itself be undone. Retention is
-- deliberately unbounded: this is one author's body of work, and a pruning policy is a decision to
-- make deliberately rather than to inherit from a default.
--
-- recorded_at is TIMESTAMP to match poems.created_at, the column it shadows. Normalising the two to
-- TIMESTAMPTZ is a rewrite of the poems table, and it is deliberately not done in the same release
-- that starts writing to it.
CREATE TABLE IF NOT EXISTS poem_versions (
    id UUID PRIMARY KEY,
    poem_id UUID NOT NULL REFERENCES poems (id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    recorded_at TIMESTAMP NOT NULL DEFAULT now(),

    -- A monotonic insertion counter, and the column the history is actually ordered by.
    --
    -- recorded_at alone is not a total order. It has microsecond resolution, so two edits landing in
    -- the same tick produce two rows with an identical timestamp, and the tiebreak falls to the id
    -- -- a random UUID, which makes the order of the history a coin flip. That is observable: two
    -- concurrent edits can leave the older draft appearing above the newer one. recorded_at is kept
    -- because it is the useful human-facing timestamp; it is simply not trusted for ordering.
    seq BIGINT GENERATED ALWAYS AS IDENTITY
);

-- Supports the only read this table has: a poem's history, newest first.
CREATE INDEX IF NOT EXISTS idx_poem_versions_poem_seq
    ON poem_versions (poem_id, seq DESC);
