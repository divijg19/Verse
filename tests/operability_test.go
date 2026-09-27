package tests

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/internal/database"
	appserver "github.com/divijg19/Verse/internal/server"
)

// This file covers the operability gaps that made a production fault expensive to diagnose.
//
// Before it, /health returned 200 without touching the database, so an outage produced an unbroken
// stream of reassuring 200s and neither a restart nor an alert. The request ID was generated, logged
// by the request logger, and then discarded -- never echoed to the client, never attached to a
// refusal -- so a report could not be tied to a log line. And the refusal body is opaque by design,
// which is correct, but it left the reason reachable only in logs that scroll away on a free
// instance.
//
// The invariant that matters most here is TestRefusalStaysOpaqueByDefault. Everything else makes a
// refusal easier to diagnose; that test is what stops those changes from making it easier for an
// attacker to diagnose.

// TestHealthReportsDatabaseReadiness pins the probe to something that can fail.
func TestHealthReportsDatabaseReadiness(t *testing.T) {
	connectTestDB(t)
	srv := newTestServer(t)

	status, body, _ := get(t, srv.URL+"/health", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /health with a reachable database = %d, want 200; body: %q", status, body)
	}
	if strings.TrimSpace(body) != "ok" {
		t.Fatalf("GET /health body = %q, want ok", body)
	}
}

// TestHealthReportsUnavailableWithoutADatabase proves the probe can return 503.
//
// The router is built without connecting a pool, which is the state a request can arrive in during
// boot and the state a crashed or disconnected pool leaves behind. If this ever returns 200, the
// probe has gone back to asserting nothing.
func TestHealthReportsUnavailableWithoutADatabase(t *testing.T) {
	// Deliberately not newTestServer: that one connects the database, which is the state this
	// test needs to be the opposite of. It also installs the package's shared client, so this
	// drives the returned server with a client of its own.
	srv := newLoginServer(t, true)
	c := newAnonymousClient(t, srv)

	resp := doGet(t, c, srv.URL+"/health")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /health with no database = %d, want 503; the probe must be able to fail",
			resp.StatusCode)
	}

	// The reason is logged, never returned. /health is unauthenticated, and a connection error
	// carries the database host and user.
	lower := strings.ToLower(string(body))
	if strings.Contains(lower, "refused") || strings.Contains(lower, "password") ||
		strings.Contains(string(body), "127.0.0.1") {
		t.Fatalf("the unauthenticated probe leaked a connection detail: %q", body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control on a 503 = %q, want no-store", got)
	}
}

// TestEveryResponseCarriesARequestID pins the correlation handle, including on the public routes and
// the error paths, because those are the ones a client can actually quote.
func TestEveryResponseCarriesARequestID(t *testing.T) {
	connectTestDB(t)
	srv := newTestServer(t)

	for _, path := range []string{"/health", "/login", "/library", "/nowhere"} {
		t.Run(path, func(t *testing.T) {
			_, _, headers := get(t, srv.URL+path, nil)
			if headers.Get("X-Request-Id") == "" {
				t.Errorf("GET %s carried no X-Request-Id, so a report cannot be tied to a log line", path)
			}
		})
	}
}

// TestRefusalCarriesARequestID proves a refusal is correlatable, which is the property that was
// missing during the incident this release addresses.
func TestRefusalCarriesARequestID(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	id := insertPoem(t, "a line worth keeping")

	// A tampered synchroniser token: refused by requireCSRF.
	status, _, headers := postForm(t, srv.URL+"/poem/update", url.Values{
		"id":      {id},
		"content": {"replacement"},
		"csrf":    {"tampered"},
	}, nil)

	if status != http.StatusForbidden {
		t.Fatalf("POST /poem/update with a tampered token = %d, want 403", status)
	}
	if headers.Get("X-Request-Id") == "" {
		t.Fatal("a refusal carried no X-Request-Id, so the log line cannot be found from the response")
	}
}

// TestRefusalStaysOpaqueByDefault is the guard on every diagnostic added in this release.
//
// With VERSE_DEBUG_LOGIN unset, a refusal must be indistinguishable whatever went wrong: same status,
// byte-exact body, and no header that names a check. If this test needs loosening, the diagnostics
// have started telling a caller which of its mistakes to fix, which is what the synchroniser token
// exists to prevent.
func TestRefusalStaysOpaqueByDefault(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	// Explicitly unset rather than merely absent, so a stray value in the environment cannot make
	// this pass for the wrong reason.
	t.Setenv("VERSE_DEBUG_LOGIN", "")

	srv := newLoginServer(t, true)
	c := newAnonymousClient(t, srv)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	token := fetchLoginToken(t, c, srv)
	resp, logged := postLogin(t, c, srv, url.Values{
		"passphrase": {testPassphrase},
		"csrf":       {token + "-tampered"},
	}, "")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if string(body) != "forbidden\n" {
		t.Errorf("refusal body = %q, want exactly %q", string(body), "forbidden\n")
	}
	if got := resp.Header.Get("X-Verse-Refusal"); got != "" {
		t.Errorf("a refusal disclosed %q in a header with diagnostics disabled", got)
	}
	// The reason must not have leaked into any other header either.
	for name := range resp.Header {
		if strings.Contains(strings.ToLower(name), "verse") && name != "X-Request-Id" {
			t.Errorf("refusal carried an unexpected Verse header: %s", name)
		}
	}
	// But it must still be in the log, or the diagnostics work is pointless. The login path has
	// its own wording, distinct from requireCSRF's, so both are checked.
	if !strings.Contains(logged, "login refused") ||
		!strings.Contains(logged, "the synchroniser cookie did not match") {
		t.Errorf("the refusal was not logged with its reason.\n  log: %s", logged)
	}
	if !strings.Contains(logged, "request ") {
		t.Errorf("the refusal log does not carry a request ID to correlate.\n  log: %s", logged)
	}
}

// TestDebugLoginNamesTheReason covers the opt-in path, and pins that it reveals a fixed word from
// this package's vocabulary rather than anything the caller chose.
func TestDebugLoginNamesTheReason(t *testing.T) {
	t.Setenv("VERSE_DEBUG_LOGIN", "1")

	srv := newLoginServer(t, true)
	c := newAnonymousClient(t, srv)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	token := fetchLoginToken(t, c, srv)
	resp, _ := postLogin(t, c, srv, url.Values{
		"passphrase": {testPassphrase},
		"csrf":       {token + "-tampered"},
	}, "")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	// The body stays opaque even with diagnostics on: the header is for operators reading a
	// response, the body is for whatever is on the other end of it.
	if string(body) != "forbidden\n" {
		t.Errorf("refusal body = %q, want exactly %q even with diagnostics enabled", string(body), "forbidden\n")
	}

	reason := resp.Header.Get("X-Verse-Refusal")
	if reason == "" {
		t.Fatal("VERSE_DEBUG_LOGIN=1 did not name the reason")
	}
	if !strings.Contains(reason, "synchroniser cookie did not match") {
		t.Errorf("X-Verse-Refusal = %q, want the token-mismatch reason", reason)
	}
}

// TestDiagnosticHeaderCannotReflectCallerInput is the security property of the opt-in header: it
// carries this package's own vocabulary, so no part of it can be chosen by whoever made the request.
func TestDiagnosticHeaderCannotReflectCallerInput(t *testing.T) {
	t.Setenv("VERSE_DEBUG_LOGIN", "1")

	srv := newLoginServer(t, true)
	c := newAnonymousClient(t, srv)
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	fetchLoginToken(t, c, srv)

	// A legal but hostile Origin. CRLF is not attempted here because Go's client refuses to send a
	// header value containing one, which is a protection in its own right but not one this test can
	// exercise. What matters is that whatever the caller sends, none of it reaches the header.
	const hostile = "https://evil.example/attacker-chosen-origin"
	resp, _ := postLogin(t, c, srv, url.Values{
		"passphrase": {testPassphrase},
		"csrf":       {"tampered"},
	}, hostile)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if resp.Header.Get("X-Injected") != "" {
		t.Fatal("a caller-controlled value reached the response headers")
	}
	reason := resp.Header.Get("X-Verse-Refusal")
	if strings.Contains(reason, "evil.example") || strings.Contains(reason, "attacker") {
		t.Fatalf("X-Verse-Refusal reflected caller input: %q", reason)
	}
	// It must still name the actual reason, so the header is useful and not merely inert.
	if reason == "" {
		t.Fatal("X-Verse-Refusal is empty, so the diagnostic header proves nothing")
	}
}

// TestHealthProbeDoesNotOutliveItsDeadline is the reason /health carries its own timeout.
//
// The router allows a request 30 seconds, and pgx holds a pooled connection for the whole of a Ping.
// With the probe inheriting the request deadline, a slow-but-alive database let up to six probes be in
// flight against a five-connection pool, after which every application query blocked waiting for a
// connection and was killed by that same 30 seconds. A slow database became a total outage.
//
// The deadline is asserted against a pool that cannot hand out a connection at all, which is the
// worst case: the probe must give up on its own schedule rather than waiting for the router.
func TestHealthProbeDoesNotOutliveItsDeadline(t *testing.T) {
	// Deliberately not newTestServer: this needs a pool that is present but unusable.
	srv := newLoginServer(t, true)
	c := newAnonymousClient(t, srv)

	start := time.Now()
	resp := doGet(t, c, srv.URL+"/health")
	elapsed := time.Since(start)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /health with an unusable pool = %d, want 503", resp.StatusCode)
	}

	// Generous, because the point is the order of magnitude, not the exact figure. The old
	// behavior would sit for the full request timeout; the new one gives up in about a second and a
	// half. A limit of 5s distinguishes them without being flaky on a loaded machine.
	if elapsed > 5*time.Second {
		t.Errorf("the probe took %s to report an unusable database; it is holding a connection "+
			"long enough to starve the pool it shares with the application", elapsed)
	}
	t.Logf("probe reported an unusable database in %s", elapsed)
}

// TestTheHealthDeadlineIsShorterThanTheRequestTimeout pins the relationship, so the two cannot drift
// back into the configuration that made a slow database an outage.
func TestTheHealthDeadlineIsShorterThanTheRequestTimeout(t *testing.T) {
	if appserver.HealthPingTimeout >= appserver.RequestTimeout {
		t.Fatalf("the health ping deadline (%s) is not shorter than the request timeout (%s); "+
			"a probe that can outlive the request can hold a pool connection for the whole request",
			appserver.HealthPingTimeout, appserver.RequestTimeout)
	}
	// And short enough to fail inside the platform's own check window, so it is reporting something
	// the platform will act on rather than being cut off mid-flight.
	if appserver.HealthPingTimeout >= 5*time.Second {
		t.Errorf("the health ping deadline is %s, which is not inside the platform's five-second "+
			"check window; a slower probe is a failed check that still holds a connection",
			appserver.HealthPingTimeout)
	}
}

// TestHealthProbeYieldsWhenThePoolIsExhausted reproduces the failure v0.4.4 introduced.
//
// The nil-pool case above returns instantly, so it proves the 503 shape but not the deadline. This
// takes every connection out of the pool and holds it, which is what a slow-but-alive database looks
// like from the pool's side: connections are checked out and not returned, and the next caller waits.
//
// Under the old behavior the probe inherited the router's 30s, so it sat waiting for a connection
// that would not come -- holding a checkout it could not complete, and reporting nothing, for half a
// minute after the platform had already given up on the check.
//
// Ordering matters here. The server is built, and its login completed, before the pool is drained:
// the login path consults the rate limiter, which lives in the database, so exhausting the pool first
// deadlocks the fixture rather than testing the probe.
func TestHealthProbeYieldsWhenThePoolIsExhausted(t *testing.T) {
	connectTestDB(t)

	srv := newTestServer(t)
	c := newAnonymousClient(t, srv)
	// Longer than the router's own timeout, so the probe's deadline is what ends the request rather
	// than this client giving up first.
	c.Timeout = 60 * time.Second

	// Now drain the pool. The size is read from the pool rather than assumed, so this still works
	// if DB_MAX_CONNS is set for the run.
	maxConns := database.Pool.Config().MaxConns
	if maxConns < 1 {
		t.Fatalf("pool reports MaxConns = %d", maxConns)
	}

	held := make([]*pgxpool.Conn, 0, maxConns)
	t.Cleanup(func() {
		for _, conn := range held {
			conn.Release()
		}
	})
	for i := int32(0); i < maxConns; i++ {
		conn, err := database.Pool.Acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire %d of %d: %v", i, maxConns, err)
		}
		held = append(held, conn)
	}
	t.Logf("holding all %d pool connection(s)", maxConns)

	start := time.Now()
	resp := doGet(t, c, srv.URL+"/health")
	elapsed := time.Since(start)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /health with an exhausted pool = %d, want 503", resp.StatusCode)
	}
	// Generous on purpose: the claim is the order of magnitude. The old code would have run to the
	// router's 30s, well past this and past any sensible platform check window.
	if elapsed > 10*time.Second {
		t.Errorf("the probe took %s with the pool exhausted; it is waiting on a scarce connection "+
			"rather than reporting on its own schedule (deadline %s)",
			elapsed, appserver.HealthPingTimeout)
	}
	t.Logf("probe reported a starved pool in %s (deadline %s)", elapsed, appserver.HealthPingTimeout)
}

// TestAnInboundRequestIDIsBoundedOnTheResponse pins the reflection, which was unbounded.
//
// middleware.RequestID honors an inbound X-Request-Id verbatim, so without a bound any caller -- not
// just an authenticated one -- chooses the contents of a response header on every route. Go's writer
// replaces CR and LF so this cannot be used for response splitting, but a megabyte of caller-supplied
// text per response is still a megabyte the service did not choose to send.
func TestAnInboundRequestIDIsBoundedOnTheResponse(t *testing.T) {
	srv := newLoginServer(t, true)
	c := newAnonymousClient(t, srv)

	t.Run("a long inbound id is truncated", func(t *testing.T) {
		headers := getWith(t, c, srv.URL+"/health", map[string]string{
			"X-Request-Id": strings.Repeat("a", 5000),
		})
		got := headers.Get("X-Request-Id")
		if got == "" {
			t.Fatal("no X-Request-Id came back; the middleware dropped it entirely")
		}
		if len(got) > 200+len("...") {
			t.Errorf("a 5000-byte inbound id came back as %d bytes on the response", len(got))
		}
		if !strings.HasSuffix(got, "...") {
			t.Errorf("a truncated id should be marked as truncated, got %q", got)
		}
	})

	t.Run("both surfaces bound the same value", func(t *testing.T) {
		// The inconsistency this fixes was the log path sanitizing and the response path not. A
		// long value exercises that without needing characters Go's client will not send: it
		// refuses any header value containing a control character, so CR, LF, tab and DEL cannot
		// be driven from a Go test at all. Truncation is the half that is reachable this way, and
		// it is the half that was actually unbounded.
		const id = "verbose-but-legal-request-identifier-with-a-lot-of-repeated-padding"
		padded := id + strings.Repeat("Z", 400)

		var respHeader string
		logged := captureLog(t, func() {
			headers := getWith(t, c, srv.URL+"/health", map[string]string{"X-Request-Id": padded})
			respHeader = headers.Get("X-Request-Id")
		})

		if len(respHeader) > 203 {
			t.Errorf("the response carried %d bytes of a 400+ byte inbound id", len(respHeader))
		}
		// A short run of Zs would survive truncation -- the bound is 200 characters, not "no Zs
		// at all" -- so the probe has to be for the whole padded run to mean anything.
		if strings.Contains(logged, strings.Repeat("Z", 200)) {
			t.Error("the log carried the inbound id unbounded, while the response bounded it; " +
				"the two surfaces disagree about what this value is")
		}
		if !strings.Contains(logged, id[:40]) {
			t.Errorf("the log lost the identifiable part of the request id.\n  log: %s", logged)
		}
	})
}

// getWith issues a GET with explicit headers on a caller-supplied client.
//
// The package's get helper goes through the shared authClient, which newLoginServer does not install.
// Using it here would panic on a nil client rather than testing anything.
func getWith(t *testing.T, c *http.Client, endpoint string, headers map[string]string) http.Header {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return resp.Header
}
