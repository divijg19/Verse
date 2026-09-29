-- Least-privilege database roles for Verse.
--
-- Run this ONCE, as an owner or superuser, against the application database. It is deliberately not
-- a migration: migrations are applied automatically at every boot by the service, and this needs a
-- credential the service should never hold. Shipping it inside migrations/ would mean every deploy
-- attempted to create roles, and a service without CREATEROLE would fail to boot.
--
-- After running it, point the service at the two credentials:
--
--   MIGRATION_DATABASE_URL   the owner (or any role with CREATE/ALTER and ownership of the tables)
--   DATABASE_URL             verse_runtime, which is what the service holds for its whole life
--
-- The service accepts MIGRATION_DATABASE_URL being unset, and falls back to DATABASE_URL for the
-- migration. That fallback exists so a deployment which has not run this script yet keeps booting;
-- while it is in use, the serving credential still holds DDL rights, and the boot log says so on
-- every start. The arrangement is opt-in and self-announcing rather than a new requirement.
--
-- RUNNING.md carries the same SQL with the Render-specific steps.

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

GRANT CONNECT ON DATABASE :"dbname" TO verse_runtime;
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
-- Default privileges apply only to objects created by the role that executed the ALTER, and the
-- migrations run as the migration role rather than as verse_runtime. Without FOR ROLE, the first
-- migration that creates a table produces one the service cannot read, and it fails during a
-- publication -- which is the scenario RISK_REGISTER R20 describes and why it is open.
--
-- FOR ROLE <migration_role> is what closes it. Replace the role name with whichever role your
-- MIGRATION_DATABASE_URL connects as; on Neon that is typically the database owner.
--
-- ALTER DEFAULT PRIVILEGES FOR ROLE verse_owner IN SCHEMA public
--     GRANT SELECT, INSERT, UPDATE ON TABLES TO verse_runtime;
-- ALTER DEFAULT PRIVILEGES FOR ROLE verse_owner IN SCHEMA public
--     GRANT USAGE ON SEQUENCES TO verse_runtime;

-- ---------------------------------------------------------------------------------------------
-- The login role.
-- ---------------------------------------------------------------------------------------------
--
-- Created with a password you choose, then used as DATABASE_URL. Keep the owner credential for
-- MIGRATION_DATABASE_URL only.
--
-- CREATE ROLE verse_app LOGIN PASSWORD 'choose-something-long';
-- GRANT verse_runtime TO verse_app;
--
-- Note that verse_app is a member of verse_runtime, so it holds exactly the grants above and
-- nothing more. Verifying that is not a matter of trust:
--
--   SET ROLE verse_app;
--   CREATE TABLE should_fail (id int);   -- ERROR: permission denied for schema public
--   SELECT count(*) FROM poems;           -- succeeds
--   DELETE FROM poems;                    -- ERROR: permission denied for table poems
--
-- The least-privilege CI job asserts exactly these three, so the property is checked on every
-- pull request rather than trusted once.
