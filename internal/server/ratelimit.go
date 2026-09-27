package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Login rate limiting.
//
// The design constraints, in the order they mattered:
//
//   - Never sleep. A delay holds a connection open and hands an attacker a cheap way to spend the
//     server's capacity. The backoff lives in the quota, and a blocked caller is refused
//     immediately. The existing code already declines to sleep on a wrong passphrase for the same
//     reason; this is the same principle applied one level up.
//   - Fail open on a database problem. If the limiter cannot reach the database, refusing every
//     login would be an outage caused by the defense itself. An attacker can therefore degrade the
//     limiter by making the database slow. That is a known and accepted trade: the passphrase is
//     still required, and the rate limit is a cost-imposer, not an authentication control.
//   - Key on a keyed hash, not a stored address. See migration 004 for why a bare hash of an IPv4
//     address would be reversible.
const (
	// rateLimitThreshold is how many failures inside the window are tolerated.
	rateLimitThreshold = 5

	// rateLimitWindow is how long failures are remembered. A caller who stops trying is forgiven.
	rateLimitWindow = 15 * time.Minute

	// rateLimitBaseBackoff is the first block. Each further failure inside the window doubles it.
	rateLimitBaseBackoff = time.Minute

	// rateLimitMaxBackoff caps the growth, so a handful of attempts cannot lock a legitimate
	// address out for hours, and cannot hold one out indefinitely.
	rateLimitMaxBackoff = 15 * time.Minute
)

// errNoPool reports that the limiter has no database. Callers log it and carry on: the limiter is
// a cost-imposer, and its absence must not become the outage.
var errNoPool = errors.New("database pool is not initialized, so login rate limiting is unavailable")

// rateLimiter is the login rate limiter. Construct with newRateLimiter.
type rateLimiter struct {
	threshold int
	window    time.Duration
	base      time.Duration
	max       time.Duration
	// now is a field rather than a direct call so the window and the backoff can be tested without
	// the test sleeping for fifteen minutes.
	now func() time.Time
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		threshold: rateLimitThreshold,
		window:    rateLimitWindow,
		base:      rateLimitBaseBackoff,
		max:       rateLimitMaxBackoff,
		now:       time.Now,
	}
}

// blockedUntil reports whether the subject is currently refused, and until when.
//
// A database error is returned rather than treated as "not blocked", so the caller can log it and
// proceed. Turning a database blip into a locked-out author is the worse failure.
func (l *rateLimiter) blockedUntil(ctx context.Context, pool *pgxpool.Pool, subject []byte) (time.Time, bool, error) {
	if pool == nil {
		// A nil pool is a real state, not only a test artifact: the login route is reachable in tests
		// and would be reachable with a mis-ordered boot. Refusing every login because the defense
		// has no database would be an outage caused by the defense, so this is reported and the
		// caller proceeds without a limit.
		return time.Time{}, false, errNoPool
	}

	const query = `SELECT blocked_until, first_failure FROM login_attempts WHERE subject = $1`

	var blockedUntil *time.Time
	var firstFailure time.Time

	err := pool.QueryRow(ctx, query, subject).Scan(&blockedUntil, &firstFailure)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read login_attempts: %w", err)
	}

	// An expired window is forgiven on read, so a caller who waits it out needs no write to recover.
	if l.now().Sub(firstFailure) > l.window {
		return time.Time{}, false, nil
	}
	if blockedUntil == nil || !l.now().Before(*blockedUntil) {
		return time.Time{}, false, nil
	}

	return *blockedUntil, true, nil
}

// recordFailure counts one failed attempt and reports the resulting block, if any.
//
// The increment is done by the database in a single statement rather than read-modify-written here.
// Two concurrent failures from the same subject would otherwise both read the same count and both
// write back the same incremented value, losing one -- and, worse, letting a caller slip past the
// threshold by racing.
func (l *rateLimiter) recordFailure(ctx context.Context, pool *pgxpool.Pool, subject []byte) (time.Time, bool, error) {
	if pool == nil {
		return time.Time{}, false, errNoPool
	}
	now := l.now()

	const upsert = `
		INSERT INTO login_attempts (subject, failures, first_failure)
		VALUES ($1, 1, $2)
		ON CONFLICT (subject) DO UPDATE
		   SET failures = CASE
		       WHEN login_attempts.first_failure < $2::timestamp - $3::interval THEN 1
		       ELSE login_attempts.failures + 1
		   END,
		       first_failure = CASE
		       WHEN login_attempts.first_failure < $2::timestamp - $3::interval THEN $2
		       ELSE login_attempts.first_failure
		   END
		RETURNING failures`

	var failures int
	if err := pool.QueryRow(ctx, upsert, subject, now, l.window).Scan(&failures); err != nil {
		return time.Time{}, false, fmt.Errorf("record login failure: %w", err)
	}

	if failures < l.threshold {
		return time.Time{}, false, nil
	}

	until := now.Add(l.backoff(failures))
	const setBlock = `UPDATE login_attempts SET blocked_until = $2 WHERE subject = $1`
	if _, err := pool.Exec(ctx, setBlock, subject, until); err != nil {
		return time.Time{}, false, fmt.Errorf("set login block: %w", err)
	}

	return until, true, nil
}

// backoff grows with each failure past the threshold, capped.
func (l *rateLimiter) backoff(failures int) time.Duration {
	backoff := l.base
	for range failures - l.threshold {
		if backoff >= l.max {
			return l.max
		}
		backoff *= 2
	}
	if backoff > l.max {
		return l.max
	}
	return backoff
}

// clear drops the subject's record, called after a successful login so a correct passphrase is not
// left carrying the failures that preceded it.
func (l *rateLimiter) clear(ctx context.Context, pool *pgxpool.Pool, subject []byte) error {
	if pool == nil {
		return errNoPool
	}
	if _, err := pool.Exec(ctx, `DELETE FROM login_attempts WHERE subject = $1`, subject); err != nil {
		return fmt.Errorf("clear login_attempts: %w", err)
	}
	return nil
}

// subjectKey derives the rate-limit key for a request.
//
// HMAC'd under the same secret that signs session tokens. Rotating that secret therefore resets
// every counter, which is correct during a rotation and unremarkable otherwise.
func subjectKey(r *http.Request, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(clientIP(r)))
	return mac.Sum(nil)
}

// clientIP returns the caller's address, preferring a proxy-reported one only when a proxy is
// actually in front.
//
// This is the part that is easy to get dangerously wrong. X-Forwarded-For is attacker-controlled on
// a directly reachable service: a client can send any value it likes, and trusting it blindly lets
// an attacker evade their own limit or aim it at somebody else.
//
// Render sets RENDER=true at runtime and terminates TLS in front of the service, appending the
// connecting address to the right of the header. Without RENDER there is no such guarantee, so
// RemoteAddr is used. In both cases the rightmost entry is taken: the leftmost is the client's own
// claim, and each hop appends to the right.
//
// The cost is stated rather than hidden. If a second proxy is ever placed in front, every caller
// through it shares one bucket. The right-side rule is the conservative choice because it cannot be
// forged, and the answer to "we need a CDN in front" is to narrow who can reach the service, not to
// widen what is trusted.
func clientIP(r *http.Request) string {
	if os.Getenv("RENDER") == "true" {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Rightmost: everything to its left was supplied by the client.
			parts := strings.Split(xff, ",")
			if candidate := strings.TrimSpace(parts[len(parts)-1]); candidate != "" {
				return candidate
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// retryAfterSeconds renders a Retry-After value.
//
// Rounded up, and never below one second: a client that retries at exactly the stated instant would
// otherwise arrive fractionally early, be refused again, and learn nothing.
func retryAfterSeconds(remaining time.Duration) int {
	secs := int((remaining + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return secs
}
