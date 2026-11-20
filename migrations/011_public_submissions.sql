-- Migration: public submission links and request rate limiting.
--
-- A Public Link is an unauthenticated capability to send text about one published work. Every send
-- is appended to public_submissions; there is no draft, no branch and no single "current" submission.
-- It is therefore deliberately separate from work_proposals, whose unique (link_id, branch) index
-- and one-applied-per-work rule serve the private editor instead.
--
-- public_links has no expiry: it lives until the author revokes it or the work is unpublished. The
-- raw token is shown once and stored only as SHA-256, exactly as for private links.
--
-- guest_requests backs a bounded, database-shared limiter for the anonymous entry and submit routes.
-- It is counters only, never content. The runtime needs INSERT, UPDATE and DELETE on it so old
-- windows can be replaced; DELETE here is not the "no hard deletes" rule, which applies to work.

CREATE TABLE IF NOT EXISTS public_links (
    id UUID PRIMARY KEY,
    poem_id UUID NOT NULL REFERENCES poems (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ NULL,
    CONSTRAINT public_links_one_per_work UNIQUE (poem_id)
);

CREATE TABLE IF NOT EXISTS public_submissions (
    id UUID PRIMARY KEY,
    link_id UUID NOT NULL REFERENCES public_links (id) ON DELETE CASCADE,
    poem_id UUID NOT NULL REFERENCES poems (id) ON DELETE CASCADE,
    guest_name TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_public_submissions_link_recent
    ON public_submissions (link_id, created_at DESC);

CREATE TABLE IF NOT EXISTS guest_requests (
    subject BYTEA NOT NULL,
    kind TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    count INTEGER NOT NULL,
    PRIMARY KEY (subject, kind)
);

DO $$
BEGIN
    IF to_regrole('verse_runtime') IS NOT NULL THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON guest_requests TO verse_runtime;
        GRANT SELECT, INSERT, UPDATE ON public_links TO verse_runtime;
        GRANT SELECT, INSERT ON public_submissions TO verse_runtime;
    END IF;
END
$$;
