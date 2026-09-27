package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/internal/migrate"
	"github.com/divijg19/Verse/internal/testsupport"
)

// These run against a real PostgreSQL, not a hand-written double. The limiter's correctness lives
// in a single UPSERT whose window-reset logic is expressed in SQL, and no double would execute it.
// A double would let the interesting half of this file pass without testing anything.

// testPool returns a pool scoped to a schema created for this test and dropped afterwards.
//
// It goes through the same destructive-test gate as the rest of the suite, and uses the same scratch
// schema helper, so a rate-limit test cannot truncate or drop schema belonging to another package.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn, reason := testsupport.DisposableDSN()
	if reason != "" {
		t.Skip(reason)
	}

	schema := testsupport.NormalizeSchemaName("ratelimit_t_", t.Name())
	pool, cleanup, err := testsupport.ConnectScratch(context.Background(), dsn, schema)
	if err != nil {
		t.Fatalf("scratch schema: %v", err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("scratch schema cleanup: %v", err)
		}
	})

	// The real schema, applied by the real runner. Hard-coding the CREATE TABLE here would let the
	// limiter tests pass against a table that migration 004 does not actually produce, which is the
	// mismatch that would only surface in production.
	if _, err := migrate.Run(context.Background(), pool); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	return pool
}

// --- 1. The threshold, the window, and the backoff ---

// TestRateLimiterBlocksAtTheThreshold walks the limiter through its whole cycle with a controlled
// clock, so the fifteen-minute window and the doubling backoff are asserted exactly rather than
// approximated with sleeps.
func TestRateLimiterBlocksAtTheThreshold(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	l := newRateLimiter()
	subject := []byte("subject-a")

	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	// Below the threshold: counted but never blocked.
	for attempt := 1; attempt < l.threshold; attempt++ {
		_, blocked, err := l.recordFailure(ctx, pool, subject)
		if err != nil {
			t.Fatalf("failure %d: %v", attempt, err)
		}
		if blocked {
			t.Fatalf("blocked after only %d failures, threshold is %d", attempt, l.threshold)
		}
		if _, isBlocked, err := l.blockedUntil(ctx, pool, subject); err != nil || isBlocked {
			t.Fatalf("reported blocked after %d failures: %v %v", attempt, isBlocked, err)
		}
		now = now.Add(time.Minute)
	}

	// The threshold-th failure blocks.
	until, blocked, err := l.recordFailure(ctx, pool, subject)
	if err != nil {
		t.Fatalf("threshold failure: %v", err)
	}
	if !blocked {
		t.Fatalf("the %dth failure did not block", l.threshold)
	}
	if got := until.Sub(now); got != l.base {
		t.Fatalf("first block = %s, want the base backoff %s", got, l.base)
	}

	// The block is visible while it lasts.
	if got, isBlocked, err := l.blockedUntil(ctx, pool, subject); err != nil || !isBlocked {
		t.Fatalf("not blocked immediately after the threshold failure: %v %v", isBlocked, err)
	} else if !got.Equal(until) {
		t.Fatalf("blockedUntil = %s, want %s", got, until)
	}

	// One more failure inside the window doubles it.
	now = until.Add(-time.Second)
	until2, blocked, err := l.recordFailure(ctx, pool, subject)
	if err != nil || !blocked {
		t.Fatalf("failure past the threshold did not block: %v %v", blocked, err)
	}
	if got := until2.Sub(now); got != 2*l.base {
		t.Fatalf("second block = %s, want %s", got, 2*l.base)
	}

	// The backoff is capped, so a determined caller cannot be held for hours by a few attempts.
	now = until2.Add(-time.Second)
	for range 20 {
		until3, _, err := l.recordFailure(ctx, pool, subject)
		if err != nil {
			t.Fatalf("extend: %v", err)
		}
		if until3.Sub(now) > l.max {
			t.Fatalf("block grew to %s, past the %s cap", until3.Sub(now), l.max)
		}
		now = until3.Add(-time.Second)
	}
}

// TestRateLimiterForgivesAfterTheWindow is the other half: the limiter must not become a permanent
// lockout, or a single unlucky afternoon ends the author's access to their own work.
//
// The window and the backoff are deliberately different lengths, and the difference is the design.
// The block is one minute; the memory is fifteen. So a blocked caller who waits out the block gets
// one more attempt inside the same window, and that attempt -- if it fails -- blocks again with a
// doubled backoff. Guessing stays expensive without ever becoming a permanent denial, and the
// escalating interval is what makes it expensive rather than merely annoying.
func TestRateLimiterForgivesAfterTheWindow(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	l := newRateLimiter()
	subject := []byte("subject-b")

	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	now := start
	l.now = func() time.Time { return now }

	// Reach the threshold, capturing the block boundary the limiter actually computed rather than
	// re-deriving it here. An earlier draft of this test recomputed it from base and was a second
	// out, which is exactly the kind of error that makes a test assert the wrong thing confidently.
	var blockEnds time.Time
	for range l.threshold {
		until, blocked, err := l.recordFailure(ctx, pool, subject)
		if err != nil {
			t.Fatalf("record: %v", err)
		}
		if blocked {
			blockEnds = until
		}
		now = now.Add(time.Second)
	}
	if blockEnds.IsZero() {
		t.Fatal("the threshold failure did not block")
	}
	blockedAt := now

	// Inside both the block and the window: refused.
	now = blockedAt
	if _, blocked, err := l.blockedUntil(ctx, pool, subject); err != nil || !blocked {
		t.Fatalf("not blocked at the moment of the block: %v %v", blocked, err)
	}
	now = blockEnds.Add(-time.Second)
	if _, blocked, err := l.blockedUntil(ctx, pool, subject); err != nil || !blocked {
		t.Fatalf("not blocked one second before the block ends: %v %v", blocked, err)
	}

	// Block expired, window not: exactly one more attempt is permitted, which is the design.
	now = blockEnds.Add(time.Second)
	if _, blocked, err := l.blockedUntil(ctx, pool, subject); err != nil || blocked {
		t.Fatalf("still blocked after the block expired, though the window remains: %v %v", blocked, err)
	}

	// And that attempt failing blocks again, for longer.
	_, blocked, err := l.recordFailure(ctx, pool, subject)
	if err != nil || !blocked {
		t.Fatalf("a failure inside the window did not re-block: %v %v", blocked, err)
	}

	// Past the window: forgiven outright, with no write needed to read that.
	now = start.Add(l.window + time.Minute)
	if _, blocked, err := l.blockedUntil(ctx, pool, subject); err != nil || blocked {
		t.Fatalf("still blocked after the window: %v %v", blocked, err)
	}

	// And the counter restarts, so a single post-window failure is not immediately a block.
	now = start.Add(l.window + 2*time.Minute)
	_, blocked, err = l.recordFailure(ctx, pool, subject)
	if err != nil {
		t.Fatalf("record after the window: %v", err)
	}
	if blocked {
		t.Fatal("a single failure after the window already blocked; the counter was not reset")
	}
}

// TestRateLimiterClearForgivesOnSuccess asserts a correct passphrase wipes the record, so a later
// slip is not punished for this one.
func TestRateLimiterClearForgivesOnSuccess(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	l := newRateLimiter()
	subject := []byte("subject-c")

	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	for range l.threshold - 1 {
		if _, _, err := l.recordFailure(ctx, pool, subject); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	if err := l.clear(ctx, pool, subject); err != nil {
		t.Fatalf("clear: %v", err)
	}

	// The threshold-th failure after a clear must not block.
	now = now.Add(time.Second)
	_, blocked, err := l.recordFailure(ctx, pool, subject)
	if err != nil {
		t.Fatalf("record after clear: %v", err)
	}
	if blocked {
		t.Fatal("blocked on the threshold-th failure despite a successful login in between")
	}
}

// TestRateLimiterCountsConcurrentFailuresExactly is the reason the increment is an UPSERT.
//
// A read-modify-write would let two concurrent failures for the same subject both read N and both
// write N+1, losing one. Losing one is not merely untidy: it means a caller who fires five
// simultaneous guesses might be counted as four and never blocked.
func TestRateLimiterCountsConcurrentFailuresExactly(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	l := newRateLimiter()
	subject := []byte("subject-d")
	l.now = func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) }

	const racers = 12
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, _, err := l.recordFailure(ctx, pool, subject); err != nil {
				t.Errorf("concurrent record: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	var failures int
	if err := pool.QueryRow(ctx, `SELECT failures FROM login_attempts WHERE subject = $1`, subject).Scan(&failures); err != nil {
		t.Fatalf("read: %v", err)
	}
	if failures != racers {
		t.Fatalf("failures = %d, want exactly %d; concurrent increments were lost", failures, racers)
	}
}

// --- 2. The key derivation, which is where a rate limiter is usually made forgeable ---

// TestClientIPIgnoresForgedForwardedFor is the security test for the whole limiter.
//
// A rate limiter keyed on a client-supplied header is not a rate limiter: the caller chooses which
// bucket they occupy. This asserts the header is ignored entirely when no proxy is known to be in
// front, and that even when one is, only the rightmost entry is believed.
func TestClientIPIgnoresForgedForwardedFor(t *testing.T) {
	cases := []struct {
		name       string
		render     bool
		remoteAddr string
		xff        string
		want       string
	}{
		{
			name:       "no proxy, no header",
			render:     false,
			remoteAddr: "203.0.113.7:5000",
			want:       "203.0.113.7",
		},
		{
			name:       "no proxy, forged header is ignored",
			render:     false,
			remoteAddr: "203.0.113.7:5000",
			xff:        "1.2.3.4",
			want:       "203.0.113.7",
		},
		{
			name:       "no proxy, a long forged chain is ignored",
			render:     false,
			remoteAddr: "203.0.113.7:5000",
			xff:        "1.2.3.4, 5.6.7.8, 9.10.11.12",
			want:       "203.0.113.7",
		},
		{
			name:       "proxy present, only the rightmost is believed",
			render:     true,
			remoteAddr: "10.0.0.1:5000",
			xff:        "1.2.3.4, 5.6.7.8",
			want:       "5.6.7.8",
		},
		{
			name:       "proxy present, a single entry is that entry",
			render:     true,
			remoteAddr: "10.0.0.1:5000",
			xff:        "5.6.7.8",
			want:       "5.6.7.8",
		},
		{
			name:       "proxy present but header empty falls back to the socket",
			render:     true,
			remoteAddr: "10.0.0.1:5000",
			xff:        "",
			want:       "10.0.0.1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RENDER", map[bool]string{true: "true", false: ""}[tc.render])

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}

			if got := clientIP(r); got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSubjectKeyIsKeyedAndStable pins the two properties the key needs: the same address always maps
// to the same bucket, and a different address never does.
func TestSubjectKeyIsKeyedAndStable(t *testing.T) {
	secret := []byte("a-test-secret-that-is-long-enough")
	other := []byte("a-different-secret-of-sufficient-length")

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", nil)
	r.RemoteAddr = "203.0.113.7:5000"
	first := subjectKey(r, secret)
	if len(first) != 32 {
		t.Fatalf("key is %d bytes, want a 32-byte SHA-256", len(first))
	}
	if string(subjectKey(r, secret)) != string(first) {
		t.Fatal("the key is not stable for the same address and secret")
	}

	r2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", nil)
	r2.RemoteAddr = "203.0.113.8:5000"
	if string(subjectKey(r2, secret)) == string(first) {
		t.Fatal("two different addresses collided on one key")
	}

	// Rotating the secret resets the counters, which is the intended behavior of using it as the
	// HMAC key, and is why the key is described as keyed rather than hashed.
	if string(subjectKey(r, other)) == string(first) {
		t.Fatal("the key does not depend on the secret")
	}

	// The key must not be the address in any recoverable form.
	if strings.Contains(string(first), "203.0.113.7") {
		t.Fatal("the key contains the address in plaintext")
	}
}

// --- 3. Degradation ---

// TestRateLimiterFailsOpenWithoutADatabase is the availability half of "fail open".
//
// The limiter's job is to make guessing expensive. If its own unavailability locks the author out of
// their own archive, it has become the outage.
func TestRateLimiterFailsOpenWithoutADatabase(t *testing.T) {
	l := newRateLimiter()
	ctx := context.Background()
	subject := []byte("subject-e")

	if _, blocked, err := l.blockedUntil(ctx, nil, subject); err == nil || blocked {
		t.Fatalf("blockedUntil without a pool = %v %v, want an error and no block", blocked, err)
	}
	if _, blocked, err := l.recordFailure(ctx, nil, subject); err == nil || blocked {
		t.Fatalf("recordFailure without a pool = %v %v, want an error and no block", blocked, err)
	}
	if err := l.clear(ctx, nil, subject); err == nil {
		t.Fatal("clear without a pool returned no error")
	}
}

// TestRetryAfterSecondsNeverRoundsDown guards the value clients are told to wait.
func TestRetryAfterSecondsNeverRoundsDown(t *testing.T) {
	cases := []struct {
		remaining time.Duration
		want      int
	}{
		{remaining: 90 * time.Second, want: 90},
		{remaining: 1500 * time.Millisecond, want: 2},
		{remaining: time.Millisecond, want: 1},
		{remaining: 0, want: 1},
		{remaining: -time.Minute, want: 1},
	}
	for _, tc := range cases {
		if got := retryAfterSeconds(tc.remaining); got != tc.want {
			t.Errorf("retryAfterSeconds(%s) = %d, want %d", tc.remaining, got, tc.want)
		}
	}
}
