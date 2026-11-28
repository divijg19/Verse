-- Least-privilege database roles for Verse.
--
-- Run this against the application database as the role MIGRATION_DATABASE_URL will use: the role
-- that runs the migrations and owns the tables. Running it as some other owner would register the
-- future-table grants against the wrong role, and the serving credential would still be denied.
-- It is deliberately not a migration: migrations are applied automatically at every boot by the
-- service, and this needs a credential the service should never hold. Shipping it inside
-- migrations/ would mean every deploy attempted to create roles, and a service without CREATEROLE
-- would fail to boot. Re-running the script adds no privileges, but it does reset verse_app's
-- password (see below), so pass the current one unless a rotation is intended.
--
-- After running it, point the service at the two credentials:
--
--   MIGRATION_DATABASE_URL   the owner (or any role with CREATE/ALTER and ownership of the tables)
--   DATABASE_URL             verse_app, a LOGIN member of verse_runtime; the service uses this
--                            credential for its whole life and never connects as verse_runtime
--
-- The service accepts MIGRATION_DATABASE_URL being unset, and falls back to DATABASE_URL for the
-- migration. That fallback exists so a deployment which has not run this script yet keeps booting;
-- while it is in use, the serving credential still holds DDL rights, and the boot log says so on
-- every start. The arrangement is opt-in and self-announcing rather than a new requirement.
--
-- RUNNING.md carries the setup procedure and the Render-specific steps, including the credential
-- split; this file is the privilege model that both CI and the operator apply.

-- ---------------------------------------------------------------------------------------------
-- The runtime role. NO LOGIN is deliberate: the role is a property of the database, and a password
-- for it would be a second secret to rotate. The login role below carries the credentials.
-- ---------------------------------------------------------------------------------------------

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'verse_runtime') THEN
        CREATE ROLE verse_runtime NOLOGIN;
    END IF;
END
$$;

-- Wrapped in a DO block because GRANT ... ON DATABASE takes a literal, not an expression.
--
-- A psql variable (:"dbname") would need a -v dbname=... on the command line, and this file is
-- documented as "run db/roles.sql as the migration role" with no such argument -- so the
-- documented procedure failed on this line with a syntax error, and the operator action that closes
-- R2 could not be completed. current_database() is always the database psql is connected to, which
-- is the one the operator connected to on purpose. %I quotes the identifier, so a database name
-- needing quoting is quoted rather than injected.
--
-- The grant is redundant on a default installation, where CONNECT is granted to PUBLIC, and it is
-- kept deliberately: it states the requirement rather than inheriting it, and it survives an
-- operator who has tightened the default.
DO $$
BEGIN
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO verse_runtime', current_database());
END
$$;
GRANT USAGE ON SCHEMA public TO verse_runtime;

-- poems: SELECT, INSERT, UPDATE.
--
-- No DELETE, and that is the interesting one. Every delete in this application is a soft delete
-- written as an UPDATE of deleted_at -- which is why that column exists at all -- so a DELETE grant
-- would grant a capability the code never uses. The one row this service really must never lose
-- cannot be destroyed through the serving credential.
GRANT SELECT, INSERT, UPDATE ON poems TO verse_runtime;

-- poem_versions: SELECT, INSERT.
--
-- No UPDATE. A retained revision is immutable by design: restoring one is performed as an ordinary
-- edit, which records the restored-over text as a new version in turn. Nothing in the application
-- modifies a version, so granting UPDATE would grant a capability the schema's own model forbids.
GRANT SELECT, INSERT ON poem_versions TO verse_runtime;

-- The sequence behind poem_versions.seq (GENERATED ALWAYS AS IDENTITY).
--
-- Required, and easy to miss: an INSERT that omits an identity column still draws from its
-- sequence, so without USAGE the insert fails at run time with "permission denied for sequence"
-- rather than at deploy time. The failure appears the first time an author edits a work, which is
-- the worst possible moment to discover a missing grant.
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO verse_runtime;

-- private_links and work_proposals: the private-link feature.
--
-- Migration 008 also grants these, conditionally on this role already existing. In the documented
-- order and in CI, migrations run first and the role does not exist yet, so that condition is false
-- and these statements are the ones that actually grant access. They are idempotent: re-running a
-- GRANT adds no new privilege.
--
-- No DELETE. Links are revoked with revoked_at and proposals are marked with applied_at; the record
-- of a capability and a suggestion is part of the work's history even after it has served its purpose.
--
-- Conditional on the tables existing for the uncommon deployment that creates roles before the
-- first migration; an unconditional GRANT would then fail on a missing relation.
DO $$
BEGIN
    IF to_regclass('public.private_links') IS NOT NULL
       AND to_regclass('public.work_proposals') IS NOT NULL THEN
        GRANT SELECT, INSERT, UPDATE ON private_links TO verse_runtime;
        GRANT SELECT, INSERT, UPDATE ON work_proposals TO verse_runtime;
    END IF;
    -- 009 adds the continuously edited draft. Its own grant covers the uncommon role-first order;
    -- this one covers the documented order where the role does not exist when 009 executes.
    IF to_regclass('public.guest_drafts') IS NOT NULL THEN
        GRANT SELECT, INSERT, UPDATE ON guest_drafts TO verse_runtime;
    END IF;
    -- 011 adds public submission links and the shared request counter.
    IF to_regclass('public.guest_requests') IS NOT NULL THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON guest_requests TO verse_runtime;
        GRANT SELECT, INSERT, UPDATE ON public_links TO verse_runtime;
        GRANT SELECT, INSERT ON public_submissions TO verse_runtime;
    END IF;
END
$$;

-- login_attempts: the full set, including DELETE.
--
-- This is the one table the service deletes from, and it is correct to: ratelimit.go clears the
-- caller's row on a successful login, so a stale failure counter does not follow them forever. The
-- table holds an HMAC of an address and counters, never work.
GRANT SELECT, INSERT, UPDATE, DELETE ON login_attempts TO verse_runtime;

-- schema_migrations is deliberately NOT granted.
--
-- The runner reads it over the migration pool, which is the owner, so the serving credential has no
-- business knowing which migrations it believes are applied. The boot log reports the result; the
-- serving process cannot query the table that records it. That is a property worth having rather
-- than an oversight -- see the note in RUNNING.md.

-- ---------------------------------------------------------------------------------------------
-- Future tables.
-- ---------------------------------------------------------------------------------------------
--
-- Default privileges attach to the role that executes the ALTER. This script is run as the
-- migration role, so no FOR ROLE is needed and no operator-specific role name belongs here: the
-- target is current_user. Without these statements, the first later migration that creates a
-- table produces one the serving credential cannot read, and the failure surfaces at request time
-- rather than at deploy time. This is the scenario RISK_REGISTER R20 describes.
--
-- Both statements are idempotent: re-running the script adds no duplicate privileges.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE ON TABLES TO verse_runtime;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE ON SEQUENCES TO verse_runtime;

-- ---------------------------------------------------------------------------------------------
-- The login role.
-- ---------------------------------------------------------------------------------------------
--
-- Created with a password you supply, then used as DATABASE_URL. Keep the owner credential for
-- MIGRATION_DATABASE_URL only.
--
-- The password arrives as a psql variable rather than being written into this file, so that the same
-- file is the one the operator runs and the one CI runs. It used to be a commented-out line reading
-- "choose-something-long", which meant the operator had to hand-edit the file before anything worked
-- -- and CI, unable to run an incomplete script, kept its own inline copy of the grants instead. Two
-- copies of the privilege model is exactly the condition this release exists to remove: the
-- documented procedure could fail while CI stayed green, and it did.
--
-- Refuse an empty password rather than creating a role with one. An empty password is a login anyone
-- can use, so a mistyped invocation must not quietly produce it.
-- Give the variable a value when it was not supplied at all, so that the check below reports "you
-- forgot it" rather than dying on an uninterpolated :'app_password' with a syntax error. The failure
-- was already safe -- nothing was created -- but a reader who forgot the argument learned nothing
-- from a parser error.
\if :{?app_password}
\else
  \set app_password ''
\endif

SELECT length(:'app_password') = 0 AS app_password_is_empty \gset
  \if :app_password_is_empty
    \echo 'ERROR: app_password is required and must not be empty.'
    \echo '  psql "$DATABASE_URL" -v app_password=choose-something-long -f db/roles.sql'
    -- A raised exception rather than \quit, and the reason is that \quit cannot do this job.
    --
    -- Verified: `\quit 1` does not set the exit status. psql's \quit takes no operand, prints
    -- "warning: \quit: extra argument "1" ignored", and exits 0 regardless. So the obvious fix is
    -- silently ineffective -- the script still prints ERROR and still reports success.
    --
    -- \quit on its own is also useless here, for the same underlying reason: it exits 0. What was
    -- wrong before this change is only that the failure was *reported* without being *signalled*, so
    -- an operator running this under `set -e` moved on believing the roles existed, and the next
    -- step was a confusing "role verse_app does not exist" rather than "you forgot the password".
    --
    -- RAISE EXCEPTION under ON_ERROR_STOP=1 is what actually makes psql exit non-zero (3). The
    -- documented command and the CI step both pass ON_ERROR_STOP=1, so the guard is effective on
    -- every path that matters. Without that flag psql continues past the error and still exits 0 --
    -- inherent to psql, not fixable from inside the script, and noted in RUNNING.md rather than left
    -- to be discovered.
    DO $$ BEGIN RAISE EXCEPTION 'app_password is required and must not be empty'; END $$;
  \endif

-- Handed to the block below as a setting rather than interpolated into it, because psql does not
-- substitute variables inside a dollar-quoted string -- it treats the whole body as a literal. The
-- alternative, a plain CREATE ROLE, is not idempotent, and a script that fails on its second run is
-- a script an operator will stop running.
SET verse.app_password = :'app_password';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'verse_app') THEN
        EXECUTE format('CREATE ROLE verse_app LOGIN PASSWORD %L',
                       current_setting('verse.app_password'));
    ELSE
        -- Reset rather than skip, so re-running with a new password rotates it. A script that is
        -- idempotent about roles but silent about credentials would leave the old password in place
        -- with no indication that the new one was ignored.
        EXECUTE format('ALTER ROLE verse_app LOGIN PASSWORD %L',
                       current_setting('verse.app_password'));
    END IF;
END
$$;

GRANT verse_runtime TO verse_app;

-- Note that verse_app is a member of verse_runtime, so it holds exactly the grants above and nothing
-- more. Verifying that is not a matter of trust:
--
--   SET ROLE verse_app;
--   CREATE TABLE should_fail (id int);   -- ERROR: permission denied for schema public
--   SELECT count(*) FROM poems;           -- succeeds
--   DELETE FROM poems;                    -- ERROR: permission denied for table poems
--
-- The Least privilege CI job applies this file and then asserts the grants that matter, including
-- the denials, so the model is checked on every pull request rather than trusted once. The job's own
-- assertions are the authority on what is checked; no count is repeated here because it goes stale.
