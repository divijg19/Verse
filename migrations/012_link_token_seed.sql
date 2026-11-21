-- Recoverable authoring links.
--
-- The raw token is still never stored: only its SHA-256 (token_hash) and a random, non-secret seed.
-- The token is recomputed from the seed and a server-held key when the author loads Share, which is
-- what lets a Copy button work on an active row after a reload. A database read alone cannot derive
-- a token from the seed; the application secret is also required.
--
-- Existing rows keep a NULL seed. Their tokens were generated before this scheme existed and cannot
-- be recovered, so they simply have no Copy button. No backfill and no data mutation: a revoked
-- link must keep failing for as long as anyone might still hold the URL, and changing rows here
-- would break live guest sessions and proposal attribution.
ALTER TABLE private_links ADD COLUMN IF NOT EXISTS token_seed TEXT NULL;
ALTER TABLE public_links  ADD COLUMN IF NOT EXISTS token_seed TEXT NULL;
