-- Migration: store the poem timestamps as instants.
--
-- poems.created_at, poems.deleted_at and poem_versions.recorded_at were declared TIMESTAMP, which in
-- Postgres means a wall clock with no zone attached. login_attempts, added later, was declared
-- TIMESTAMPTZ, and 004_login_rate_limit.sql explains at length why that is the correct choice for a
-- column that records *when something happened*: a zone-less value read back into Go is interpreted
-- as UTC, while the value written was Go's local wall clock, so on any host that is not UTC every
-- comparison between a stored instant and the current time is wrong by the zone offset. That was not
-- hypothetical there -- it was caught by a Retry-After of 19855 seconds where the cap is 900, and
-- 19855 is very nearly the +05:30 offset.
--
-- This migration is the other half of that reasoning, deferred until it could be done alone. Both
-- 002 and 005 record the deferral: normalising these columns "is a rewrite of the poems table, and it
-- is deliberately not done in the same release that starts writing to it." This is that release.
--
-- WHY EVERY ALTER CARRIES AN EXPLICIT USING CLAUSE
--
-- The obvious form of this migration is:
--
--     ALTER TABLE poems ALTER COLUMN created_at TYPE TIMESTAMPTZ;
--
-- and it is wrong. Converting zone-less to zoned reinterprets the stored value *in the session's
-- current time zone*, not in UTC. A database running at +05:30 would reinterpret
-- 2024-01-01 00:00:00 as 2024-01-01 00:00:00+05:30, which is 2023-12-31 18:30:00 UTC -- silently
-- moving every poem in the library by five and a half hours, and moving them by a *different* amount
-- depending on when the migration happened to run.
--
-- AT TIME ZONE 'UTC' states the interpretation explicitly, so the result is identical on a server
-- set to UTC, one set to Asia/Kolkata, and one set to America/Los_Angeles. That is the whole point:
-- the conversion's meaning should come from this file, not from ambient server configuration that
-- nobody reading the schema will know to check. TestTimestampMigrationIsZoneIndependent runs the real
-- file under a non-UTC session and asserts the instants are unchanged, so a later edit that drops the
-- clause fails the build rather than the library.
--
-- WHAT THIS ASSUMES, AND HOW TO CHECK IT BEFORE DEPLOYING
--
-- The clause encodes one assumption: that the stored zone-less values are UTC wall clocks.
--
-- poems.created_at and poem_versions.recorded_at are populated solely by DEFAULT now(). now() is
-- TIMESTAMPTZ, and assigning it into a TIMESTAMP column converts it to the session's local wall clock
-- first -- so the values in the table are UTC only because the database has been running in UTC. On
-- the hosted database that has always been true. On a restored dump, a branch, or a self-hosted
-- instance it may not be, and no migration can detect that: the zone information was discarded on
-- the way in, and what remains looks exactly like a UTC wall clock.
--
-- So this is the preflight, and it is worth running before deploying rather than after wondering:
--
--     SELECT count(*) FILTER (WHERE created_at IS NULL) AS null_created_at,
--            min(created_at)                       AS earliest,
--            max(created_at)                       AS latest
--     FROM poems;
--
-- A non-zero null_created_at is not a problem for this migration -- see below -- but the earliest and
-- latest values are worth a glance. If they are consistent with when the work was actually written,
-- the assumption holds. If they are shifted by a whole zone offset, this database's history is not in
-- UTC, and the correct fix is to correct the rows before converting them, not after. Take an export
-- first either way; see docs/RUNNING.md.
--
-- NULLs PASS THROUGH UNCHANGED, DELIBERATELY
--
-- created_at is nullable: it has a default but was never declared NOT NULL, so a row inserted with an
-- explicit NULL is representable. Converting TIMESTAMP to TIMESTAMPTZ maps NULL to NULL, and this
-- migration deliberately does not fail on one and deliberately does not invent a value for it.
-- Backfilling to now() would fabricate a creation instant the author never chose, and it would do so
-- silently -- the work would appear on the heatmap for a day it was never written. Failing the
-- migration instead would block a deploy over data the application has no way to repair, which is a
-- worse outcome than a row that is absent from the heatmap and obviously so.
--
-- created_at is not promoted to NOT NULL here. That is a separate constraint decision, and folding it
-- in would make this release able to fail for a reason the previous one could not.
--
-- schema_migrations.applied_at IS DELIBERATELY LEFT AS TIMESTAMP
--
-- It is the one remaining zone-less timestamp in the schema, and it is left alone for three reasons.
-- Nothing reads it: loadApplied selects only filename and checksum. It records when a migration was
-- applied, which is an operational fact about the deploy rather than something the application
-- interprets. And the runner's own comment on the bookkeeping DDL states that these column
-- definitions "must never change incompatibly", because the table is created with IF NOT EXISTS and
-- may already exist in a database managed by hand or by an older version of the application. Changing
-- a column that the process depends on, in the same release that rewrites the tables it references,
-- buys a tidiness that is not worth the risk.
--
-- The ALTERs rewrite their tables and rebuild their indexes. That is why this is a release of its own,
-- and why the runner bounds it: see internal/migrate/migrate.go for the lock and statement timeouts
-- that make a blocked ALTER fail loudly instead of hanging a zero-downtime deploy.
ALTER TABLE poems
    ALTER COLUMN created_at TYPE TIMESTAMPTZ USING created_at AT TIME ZONE 'UTC',
    ALTER COLUMN created_at SET DEFAULT now();

ALTER TABLE poems
    ALTER COLUMN deleted_at TYPE TIMESTAMPTZ USING deleted_at AT TIME ZONE 'UTC';

ALTER TABLE poem_versions
    ALTER COLUMN recorded_at TYPE TIMESTAMPTZ USING recorded_at AT TIME ZONE 'UTC',
    ALTER COLUMN recorded_at SET DEFAULT now();
