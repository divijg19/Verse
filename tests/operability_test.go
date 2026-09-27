package tests

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
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
