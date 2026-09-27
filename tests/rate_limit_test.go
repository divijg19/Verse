package tests

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/testsupport"
)

// End-to-end rate limiting, driven over HTTP the way a caller would drive it.
//
// The unit tests in internal/server cover the counter, the window and the backoff against a real
// database. What they cannot cover is the part a caller experiences: the 429, the Retry-After
// header, and -- most importantly -- that a *correct* passphrase is still refused while the block
// holds, so the limiter cannot be used to deny the owner access to their own archive.
//
// These set RENDER=true and address callers through X-Forwarded-For, because that is the path
// production takes and it is the only way to present two distinct callers to one listener. A real
// TCP connection cannot be given a second source address, and pretending otherwise would test a path
// the service never takes.

// clearLoginAttempts empties the rate-limit table for a test.
//
// The table deliberately persists between runs, exactly as it does in production, so without this
// the counter carries over and a later run of the same test starts part-way to its threshold. That
// surfaced as a test that passed once and then failed on the second run with a 429 on a correct
// passphrase.
//
// It is a separate helper from truncatePoems on purpose: that one is about the poems table, and
// folding an unrelated table into it would couple two things that have no reason to move together.
func clearLoginAttempts(t *testing.T) {
	t.Helper()

	if _, reason := testsupport.DisposableDSN(); reason != "" {
		t.Skip(reason)
	}
	if database.Pool == nil {
		t.Fatal("database pool is nil")
	}
	if _, err := database.Pool.Exec(t.Context(), `TRUNCATE login_attempts`); err != nil {
		t.Fatalf("truncate login_attempts: %v", err)
	}
}

// loginFrom posts the login form as if it came from the given forwarded address.
func loginFrom(t *testing.T, srv *httptest.Server, c *http.Client, forwarded, passphrase, csrf string) *http.Response {
	t.Helper()

	form := url.Values{"passphrase": {passphrase}, "csrf": {csrf}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/login",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build POST: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", forwarded)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST /login from %s: %v", forwarded, err)
	}
	return resp
}

// loginFormFor fetches a fresh login page as the given forwarded address and returns the token the
// client should submit.
func loginFormFor(t *testing.T, srv *httptest.Server, c *http.Client, forwarded string) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/login", nil)
	if err != nil {
		t.Fatalf("build GET: %v", err)
	}
	req.Header.Set("X-Forwarded-For", forwarded)

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET /login from %s: %v", forwarded, err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /login from %s = %d, want 200", forwarded, resp.StatusCode)
	}

	if token := csrfFromJarFor(t, c, srv.URL); token != "" {
		return token
	}
	return csrfFromFormBody(t, string(page))
}

// noRedirect is the redirect policy every rate-limit test needs: a followed 303 would mask the
// status under test behind whatever the destination happens to return.
func noRedirect() func(*http.Request, []*http.Request) error {
	return func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
}

// burnQuota drives wrong passphrases from an address until the service refuses, and reports whether
// it did.
//
// The negative assertions in this file -- "this caller is not rate limited" -- would all pass if the
// limiter were simply absent. They pass today precisely because database.Pool is nil in a test that
// never connects, and the limiter correctly fails open. Every such test therefore calls this first,
// so a missing limiter fails loudly instead of making the safety assertions look proven.
func burnQuota(t *testing.T, srv *httptest.Server, c *http.Client, addr string) bool {
	t.Helper()

	for attempt := range 10 {
		csrf := loginFormFor(t, srv, c, addr)
		resp := loginFrom(t, srv, c, addr, "wrong-"+strconv.Itoa(attempt), csrf)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			return true
		}
	}
	return false
}

// TestLoginIsRateLimitedAfterRepeatedFailures is the behavior the release exists for.
//
// Wrong passphrases until the service refuses outright, then the *correct* passphrase, which must
// still be refused. A limiter that forgives on a correct guess is not a limiter, and one that lets a
// correct guess through mid-block is a denial-of-service tool aimed at the owner.
func TestLoginIsRateLimitedAfterRepeatedFailures(t *testing.T) {
	// The trusted-proxy branch, which is what Render runs behind.
	t.Setenv("RENDER", "true")

	// The limiter lives in the database, so these need one. Without it the limiter fails open
	// and every rate-limit assertion below would be vacuous.
	connectTestDB(t)
	clearLoginAttempts(t)

	srv := newTestServer(t)
	anon := newAnonymousClient(t, srv)
	anon.CheckRedirect = noRedirect()

	const caller = "203.0.113.10"
	limited := false

	for attempt := range 10 {
		csrf := loginFormFor(t, srv, anon, caller)
		resp := loginFrom(t, srv, anon, caller, "wrong-"+strconv.Itoa(attempt), csrf)
		status := resp.StatusCode
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if status == http.StatusTooManyRequests {
			limited = true
			break
		}
		// Any other status is a behavior change worth failing on, so this loop cannot pass by
		// accident if the handler starts refusing for an unrelated reason.
		if status != http.StatusOK {
			t.Fatalf("attempt %d: status = %d, want 200 (rejection) or 429", attempt, status)
		}
		if !strings.Contains(string(body), "not accepted") {
			t.Fatalf("attempt %d: response did not render the rejection message; body: %s",
				attempt, truncate(body))
		}
	}

	if !limited {
		t.Fatal("the service never returned 429 after ten wrong passphrases")
	}

	// The correct passphrase must still be refused while the block holds.
	csrf := loginFormFor(t, srv, anon, caller)
	resp := loginFrom(t, srv, anon, caller, testPassphrase, csrf)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("a correct passphrase during a block returned %d, want 429; body: %s",
			resp.StatusCode, truncate(body))
	}
	if sessionFromJarFor(t, anon, srv.URL) != "" {
		t.Fatal("a session was established while the caller was rate limited")
	}
}

// TestRateLimitResponsesCarryRetryAfter checks the header a client is told to respect, and that the
// body does not hand out the counter.
func TestRateLimitResponsesCarryRetryAfter(t *testing.T) {
	t.Setenv("RENDER", "true")

	// The limiter lives in the database, so these need one. Without it the limiter fails open
	// and every rate-limit assertion below would be vacuous.
	connectTestDB(t)
	clearLoginAttempts(t)

	srv := newTestServer(t)
	anon := newAnonymousClient(t, srv)
	anon.CheckRedirect = noRedirect()

	const caller = "203.0.113.20"
	for attempt := range 10 {
		csrf := loginFormFor(t, srv, anon, caller)
		resp := loginFrom(t, srv, anon, caller, "wrong-"+strconv.Itoa(attempt), csrf)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			continue
		}

		retry := resp.Header.Get("Retry-After")
		if retry == "" {
			t.Fatal("a 429 carried no Retry-After header")
		}
		secs, err := strconv.Atoi(retry)
		if err != nil {
			t.Fatalf("Retry-After = %q, want a whole number of seconds", retry)
		}
		if secs < 1 {
			t.Fatalf("Retry-After = %d, want at least 1 so a client does not retry immediately", secs)
		}
		if secs > 900 {
			t.Fatalf("Retry-After = %d, past the 15-minute cap", secs)
		}
		// The counter is a hint about how close a caller is to the threshold, so the body must carry
		// no digits at all. An earlier version of this check looked for the words "failures" and
		// "attempts", which flagged the legitimate message "too many failed attempts" -- checking
		// for a digit tests the thing that actually matters.
		if strings.ContainsAny(string(body), "0123456789") {
			t.Fatalf("the 429 body leaks a number: %q", string(body))
		}
		return
	}

	t.Fatal("the service never returned 429")
}

// TestRateLimiterIsPerCaller asserts two callers do not share a bucket.
//
// Without this, one caller burning the quota would lock out everybody else, which on a
// single-author service is the difference between a nuisance and a total outage.
func TestRateLimiterIsPerCaller(t *testing.T) {
	t.Setenv("RENDER", "true")

	// The limiter lives in the database, so these need one. Without it the limiter fails open
	// and every rate-limit assertion below would be vacuous.
	connectTestDB(t)
	clearLoginAttempts(t)

	srv := newTestServer(t)

	attacker := newAnonymousClient(t, srv)
	attacker.CheckRedirect = noRedirect()
	const attackerAddr = "203.0.113.30"

	// Assert the limiter really fired. Without this the owner assertion below would hold for the
	// trivial reason that nobody was limited at all.
	if !burnQuota(t, srv, attacker, attackerAddr) {
		t.Fatal("the attacker was never rate limited, so the per-caller assertion below proves nothing")
	}

	owner := newAnonymousClient(t, srv)
	owner.CheckRedirect = noRedirect()
	const ownerAddr = "198.51.100.4"
	csrf := loginFormFor(t, srv, owner, ownerAddr)
	resp := loginFrom(t, srv, owner, ownerAddr, testPassphrase, csrf)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("a different caller got %d after another address was rate limited, want 303; body: %s",
			resp.StatusCode, truncate(body))
	}
	if sessionFromJarFor(t, owner, srv.URL) == "" {
		t.Fatal("the unaffected caller did not get a session")
	}
}

// TestRateLimitForgivesAfterASuccessfulLogin is the other side of the per-caller test: a caller who
// succeeds must not be carrying their earlier failures forward.
func TestRateLimitForgivesAfterASuccessfulLogin(t *testing.T) {
	t.Setenv("RENDER", "true")

	// The limiter lives in the database, so these need one. Without it the limiter fails open
	// and every rate-limit assertion below would be vacuous.
	connectTestDB(t)
	clearLoginAttempts(t)

	srv := newTestServer(t)
	c := newAnonymousClient(t, srv)
	c.CheckRedirect = noRedirect()

	const caller = "203.0.113.40"

	// A few failures, then the correct passphrase, then failures again. The second run of failures
	// starts from zero, so it cannot reach the threshold.
	for attempt := range 3 {
		csrf := loginFormFor(t, srv, c, caller)
		resp := loginFrom(t, srv, c, caller, "wrong-"+strconv.Itoa(attempt), csrf)
		resp.Body.Close()
	}

	csrf := loginFormFor(t, srv, c, caller)
	resp := loginFrom(t, srv, c, caller, testPassphrase, csrf)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the correct passphrase returned %d, want 303", resp.StatusCode)
	}

	// The session is now set, so start a clean client on the same address to observe the counter.
	fresh := newAnonymousClient(t, srv)
	fresh.CheckRedirect = noRedirect()
	for attempt := range 3 {
		csrf := loginFormFor(t, srv, fresh, caller)
		resp := loginFrom(t, srv, fresh, caller, "wrong-"+strconv.Itoa(attempt), csrf)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("blocked after %d failures on a counter that a success had cleared", attempt+1)
		}
	}

	// And the counter is genuinely live on this address: a full quota still limits. This is what
	// makes the three failures above meaningful rather than merely unblocked.
	if !burnQuota(t, srv, fresh, caller) {
		t.Fatal("the limiter never limited this address at all; the clearing assertion proved nothing")
	}
}
