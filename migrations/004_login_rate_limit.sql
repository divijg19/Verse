-- Rate-limit failed login attempts, keyed by a keyed hash of the client address.
--
-- The key is an HMAC under the same secret that signs session tokens, not a bare SHA-256 of the
-- address. A bare hash of an IPv4 address is reversible in microseconds by precomputing the entire
-- space, so it would store the addresses in a form anyone with the table could recover. The HMAC is
-- only as strong as the secret, and rotating the secret resets every counter, which is the correct
-- behaviour anyway.
--
-- `failures` and `first_failure` implement a fixed window: attempts older than the window are
-- forgotten by resetting the row, rather than being counted forever.
--
-- `blocked_until` is set once the threshold is reached and extended on each subsequent failure
-- inside the window, so a continuing attempt lengthens the block without a separate counter.
--
-- One row per subject, so the table is bounded by the number of distinct callers rather than by
-- the number of attempts. There is no index beyond the primary key: every query is by key.
CREATE TABLE IF NOT EXISTS login_attempts (
	subject       BYTEA PRIMARY KEY,
	failures      INTEGER     NOT NULL DEFAULT 0,
	-- TIMESTAMPTZ, not TIMESTAMP. A timestamp without a zone read back into Go is interpreted as UTC
	-- while the value written was Go's local wall clock, so on any host that is not UTC every
	-- comparison between a stored instant and the current time is wrong by the zone offset. That is
	-- not hypothetical: it was caught here by a Retry-After of 19855 seconds where the cap is 900,
	-- and 19855 is very nearly the +05:30 offset. An instant is what this column means, so it is
	-- stored as one and normalised by the database.
	first_failure TIMESTAMPTZ NOT NULL DEFAULT now(),
	blocked_until TIMESTAMPTZ
);
