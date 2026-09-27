package tests

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// This file reproduces a production login failure exactly, and then pins the fix.
//
// The fault, from the deployed instance's log on 2026-09-27:
//
//	09:57:33 GET  /login  200 3722B
//	09:57:54 login refused: cross-origin submission; scheme=https origin=null
//	09:57:54 POST /login  403 10B
//
// The login page rendered, the passphrase was correct, and the submission was still refused. The
// reason is that the client sent the literal header `Origin: null`, which sameOrigin does not match
// against the request host, so verifyLoginCSRF rejected it at the Origin check and never reached the
// synchroniser comparison that is the actual control.
//
// `Origin: null` is what a browser sends when it considers the request's origin opaque. The obvious
// candidate -- a sandboxed iframe -- is impossible here, because the security headers set
// X-Frame-Options: DENY and a CSP with no frame-ancestors, so the login page cannot be framed by
// anything. The remaining producers are a file:// page, a privacy extension or hardened browser
// setting, or a test client setting the header explicitly. None of them is an attack, and all of them
// are a real user being locked out of their own writing.
//
// The fix is to stop treating an opaque origin as a cross-origin claim, and let the synchroniser token
// decide. That is only safe if the token really is sufficient, which is what T2 and T3 below prove:
// a bad token is still refused, and a genuine cross-origin Origin is still refused. A test suite that
// only asserted the happy path would pass just as well with the check deleted entirely.

// prodHost and prodForwardedProto are the authority and scheme the deployed instance saw, reproduced
// so the test exercises the same code path production did.
const (
	prodHost           = "verse-0vt7.onrender.com"
	prodForwardedProto = "https"
)

// opaqueOrigin is the literal header value the production client sent. It is not the empty string:
// an absent Origin header is treated as "no claim made" and has always been allowed through.
const opaqueOrigin = "null"

// doPostFormWithHeaders is doPostForm with control over extra headers.
//
// The Host header is deliberately left alone, even though production saw verse-0vt7.onrender.com.
// Overriding it costs the synchroniser cookie: Go's client keys the cookie jar off the request URL,
// but a rewritten Host leaves the jar and the server disagreeing about the authority, and the cookie
// silently vanishes -- which shows up as a 403 for "no synchroniser cookie" and looks like the very
// fault this file exists to fix. Nothing here depends on the host's value anyway: "null" and
// "https://evil.example" fail to match any authority, and the absent-Origin case skips the check
// before the host is ever read.
func doPostFormWithHeaders(t *testing.T, c *http.Client, endpoint string, form url.Values,
	headers map[string]string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", endpoint, err)
	}
	return resp
}

// fetchLoginToken renders the login page and returns the token a browser would submit, parsed from
// the HTML rather than read out of the cookie jar.
func fetchLoginToken(t *testing.T, c *http.Client, srv *httptest.Server) string {
	t.Helper()

	resp := doGet(t, c, srv.URL+"/login")
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", resp.StatusCode)
	}
	return csrfFromFormBody(t, string(page))
}

// postLogin submits the login form under the given Origin, returning the response and the log.
func postLogin(t *testing.T, c *http.Client, srv *httptest.Server, form url.Values,
	origin string) (*http.Response, string) {
	t.Helper()

	headers := map[string]string{"X-Forwarded-Proto": prodForwardedProto}
	if origin != "" {
		headers["Origin"] = origin
	}

	var resp *http.Response
	logged := captureLog(t, func() {
		resp = doPostFormWithHeaders(t, c, srv.URL+"/login", form, headers)
	})
	return resp, logged
}

// TestLoginOriginPolicy pins the whole origin policy on the login path, including the production
// fault. Read the cases in order: T1 is the bug, T2 and T3 are why fixing it is safe, T4 is the
// behavior that must not change.
func TestLoginOriginPolicy(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		// tamper replaces the submitted token with a wrong one.
		tamper bool
		want   int
	}{
		{
			// T1. The production fault. A correct passphrase, a correct token, and an opaque
			// origin: refused, and the user locked out of their own writing.
			name:   "opaque origin with a valid token is accepted",
			origin: opaqueOrigin,
			want:   http.StatusSeeOther,
		},
		{
			// T2. The security proof. The synchroniser comparison is the actual control, so
			// relaxing the Origin check must not let a wrong token through. If this ever passes
			// with a 200 or 303, the fix has opened a login CSRF and the check must be reinstated.
			name:   "opaque origin with a tampered token is still refused",
			origin: opaqueOrigin,
			tamper: true,
			want:   http.StatusForbidden,
		},
		{
			// T3. The non-regression proof. A genuine cross-origin request is still turned away
			// outright, and never reaches the token comparison.
			name:   "a real cross-origin request is still refused",
			origin: "https://evil.example",
			want:   http.StatusForbidden,
		},
		{
			// T4. No Origin header at all has always meant "no claim made" and must keep working.
			name:   "an absent origin with a valid token is accepted",
			origin: "",
			want:   http.StatusSeeOther,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newLoginServer(t, true)
			c := newAnonymousClient(t, srv)
			c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

			token := fetchLoginToken(t, c, srv)
			if csrfFromJarFor(t, c, srv.URL) == "" {
				t.Fatal("the login page did not set a synchroniser cookie, so this case proves nothing")
			}

			csrf := token
			if tc.tamper {
				csrf = token + "-tampered"
			}
			form := url.Values{"passphrase": {testPassphrase}, "csrf": {csrf}}

			resp, logged := postLogin(t, c, srv, form, tc.origin)
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != tc.want {
				t.Fatalf("POST /login with Origin=%q = %d, want %d; body: %s\n  server log: %s",
					tc.origin, resp.StatusCode, tc.want, truncate(body), logged)
			}

			// Whatever the outcome, a refusal must stay opaque: the body is compared byte for
			// byte, because "forbidden\n" is the contract with the client and anything longer
			// would tell an unauthenticated caller which of its mistakes to fix.
			if tc.want == http.StatusForbidden {
				if got := string(body); got != "forbidden\n" {
					t.Fatalf("the refusal body is %q, want exactly %q", got, "forbidden\n")
				}
			}
		})
	}
}

// TestOpaqueOriginPolicyOnAuthenticatedMutations covers the second half of the fix.
//
// requireCSRF carried the identical Origin check, so a client with an opaque origin that could log in
// would still be refused on the first poem it tried to save. Fixing login alone would have moved the
// 403 one form further along rather than removing it.
func TestOpaqueOriginPolicyOnAuthenticatedMutations(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	cases := []struct {
		name   string
		origin string
		tamper bool
		want   int
	}{
		{
			name:   "updating a poem with an opaque origin is accepted",
			origin: opaqueOrigin,
			want:   http.StatusOK,
		},
		{
			name:   "updating a poem with an opaque origin and a tampered token is still refused",
			origin: opaqueOrigin,
			tamper: true,
			want:   http.StatusForbidden,
		},
		{
			name:   "updating a poem from a real cross-origin request is still refused",
			origin: "https://evil.example",
			want:   http.StatusForbidden,
		},
		{
			name:   "updating a poem with an absent origin is accepted",
			origin: "",
			want:   http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := insertPoem(t, "Before the thaw")
			srv := newTestServer(t)

			// postForm injects the session-bound token when none is given, which is the rendered
			// form's behavior. A tampered token is set explicitly so the comparison is exercised.
			values := url.Values{"id": {id}, "content": {"After the thaw"}}
			if tc.tamper {
				values.Set("csrf", "tampered")
			}
			headers := map[string]string{}
			if tc.origin != "" {
				headers["Origin"] = tc.origin
			}

			status, body, _ := postForm(t, srv.URL+"/poem/update", values, headers)

			if status != tc.want {
				t.Fatalf("POST /poem/update with Origin=%q = %d, want %d; body: %s",
					tc.origin, status, tc.want, truncate([]byte(body)))
			}
			if tc.want == http.StatusForbidden && body != "forbidden\n" {
				t.Fatalf("the refusal body is %q, want exactly %q", body, "forbidden\n")
			}
			if tc.want == http.StatusOK && poemContentByID(t, id) != "After the thaw" {
				t.Fatal("a 200 from /poem/update did not persist the change")
			}
		})
	}
}
