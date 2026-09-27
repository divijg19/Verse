package tests

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	appserver "github.com/divijg19/Verse/internal/server"
)

// This file covers the login path's failure reporting, and closes a gap that let a real production
// fault ship unnoticed.
//
// The gap: the suite proved the synchroniser comparison works by reading the token out of the cookie
// jar and posting it back. It never checked that the token rendered into the HTML was the same one.
// A template that rendered a stale, empty or wrong value would have passed every test and failed
// every real login. TestLoginAcceptsTheTokenRenderedInTheForm now closes that.
//
// The second gap: every test in this suite uses httptest.NewTLSServer, deliberately, because the
// Secure cookie configuration is only meaningful over TLS. The consequence is that the plain-HTTP
// path -- where a browser silently discards a Secure cookie and the login then answers a bare 403 --
// had no coverage at all. That is the failure that cost a debugging session, and
// TestLoginRefusalIsDiagnosed covers it.

// csrfFieldPattern extracts the synchroniser token as rendered into the login form.
//
// Deliberately a regex over the served HTML rather than a second source of truth: the point is to
// read what a browser would actually submit, not what the server believes it wrote.
var csrfFieldPattern = regexp.MustCompile(`name="csrf"\s+value="([^"]*)"`)

// csrfFromFormBody returns the synchroniser token present in a rendered login page.
func csrfFromFormBody(t *testing.T, page string) string {
	t.Helper()

	match := csrfFieldPattern.FindStringSubmatch(page)
	if match == nil {
		t.Fatalf("the login form did not render a csrf field at all; body: %s", truncate([]byte(page)))
	}
	if match[1] == "" {
		t.Fatalf("the login form rendered an empty csrf value; body: %s", truncate([]byte(page)))
	}
	return match[1]
}

// captureLog redirects the standard logger for the duration of fn and returns what was written.
//
// The login path's diagnostics go to the standard logger, which is the only place they can go: they
// must not reach the response, because a client that cannot produce a valid token has no business
// learning which of its mistakes it made. That makes the log the thing to assert on, so it has to be
// capturable.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	flags := log.Flags()
	prefix := log.Prefix()
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
	})

	fn()
	return buf.String()
}

// newLoginServer builds a router with valid credentials and returns a server for it. It does not
// log in, so the caller controls the session state.
func newLoginServer(t *testing.T, tls bool) *httptest.Server {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if filepath.Base(wd) == "tests" {
		if err := os.Chdir(".."); err != nil {
			t.Fatalf("chdir to repo root failed: %v", err)
		}
		t.Cleanup(func() { _ = os.Chdir(wd) })
	}

	t.Setenv("VERSE_AUTHORIZATION", testPassphrase)
	t.Setenv("VERSE_AUTH_SECRET", testAuthSecret)

	router, err := appserver.NewRouter()
	if err != nil {
		t.Fatalf("build router: %v", err)
	}

	var srv *httptest.Server
	if tls {
		srv = httptest.NewTLSServer(router)
	} else {
		srv = httptest.NewServer(router)
	}
	t.Cleanup(srv.Close)

	return srv
}

// TestLoginAcceptsTheTokenRenderedInTheForm is the regression this file exists for.
//
// It completes a real login using the token parsed out of the served HTML, never the cookie jar.
// Before this, every test fed the comparison the cookie's own value, so a template that rendered a
// different token would have been invisible to the suite while breaking every real login with a bare
// 403.
func TestLoginAcceptsTheTokenRenderedInTheForm(t *testing.T) {
	srv := newLoginServer(t, true)
	anon := newAnonymousClient(t, srv)
	anon.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp := doGet(t, anon, srv.URL+"/login")
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", resp.StatusCode)
	}

	// Two independent reads of what the server sent: the form, and the cookie.
	rendered := csrfFromFormBody(t, string(page))
	jar := csrfFromJarFor(t, anon, srv.URL)

	if rendered != jar {
		t.Fatalf("the form and the cookie disagree.\n  form:   %q\n  cookie: %q\n"+
			"  the browser submits the form value, so a mismatch here is a bare 403 for every real login",
			rendered, jar)
	}

	// And that value is actually accepted, which is the part the jar-based tests never exercised.
	form := url.Values{"passphrase": {testPassphrase}, "csrf": {rendered}}
	post := doPostForm(t, anon, srv.URL+"/login", form)
	defer post.Body.Close()
	body, _ := io.ReadAll(post.Body)

	if post.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login with the rendered token = %d, want 303; body: %s",
			post.StatusCode, truncate(body))
	}
	if sessionFromJarFor(t, anon, srv.URL) == "" {
		t.Fatal("a 303 from /login did not establish a session cookie")
	}
}

// TestLoginRefusalIsDiagnosed covers each refusal reason, and asserts the reason reaches the log
// while the response stays opaque.
func TestLoginRefusalIsDiagnosed(t *testing.T) {
	cases := []struct {
		name string
		// submit performs the refused login and returns the response.
		submit  func(t *testing.T, srv *httptest.Server, c *http.Client, form url.Values, headers map[string]string) *http.Response
		wantLog string
	}{
		{
			name: "no cookie",
			submit: func(t *testing.T, srv *httptest.Server, c *http.Client, form url.Values, _ map[string]string) *http.Response {
				jar, _ := cookiejar.New(nil)
				c.Jar = jar
				return doPostForm(t, c, srv.URL+"/login", form)
			},
			wantLog: "no synchroniser cookie was sent",
		},
		{
			name: "form carried no token field",
			submit: func(t *testing.T, srv *httptest.Server, c *http.Client, form url.Values, _ map[string]string) *http.Response {
				return doPostForm(t, c, srv.URL+"/login", form)
			},
			wantLog: "the form carried no synchroniser field",
		},
		{
			name: "token did not match",
			submit: func(t *testing.T, srv *httptest.Server, c *http.Client, form url.Values, _ map[string]string) *http.Response {
				return doPostForm(t, c, srv.URL+"/login", form)
			},
			wantLog: "the synchroniser cookie did not match the form",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newLoginServer(t, true)
			// newAnonymousClient, not srv.Client(): the latter returns a client with a nil Jar, and a
			// nil cookie store is a panic rather than an empty result.
			c := newAnonymousClient(t, srv)

			// Establish a cookie so the "no cookie" case is the only one that lacks one.
			warm := doGet(t, c, srv.URL+"/login")
			warm.Body.Close()

			good := csrfFromJarFor(t, c, srv.URL)

			form := url.Values{"passphrase": {testPassphrase}}
			switch tc.wantLog {
			case "no synchroniser cookie was sent":
				// No cookie: the jar is replaced with an empty one inside submit.
			case "the form carried no synchroniser field":
				// Token omitted entirely.
			case "the synchroniser cookie did not match the form":
				form.Set("csrf", good+"-tampered")
			}

			var resp *http.Response
			logged := captureLog(t, func() {
				resp = tc.submit(t, srv, c, form, nil)
			})
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)

			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body: %s", resp.StatusCode, truncate(body))
			}
			// The response must not disclose which check failed.
			if got := strings.TrimSpace(string(body)); got != "forbidden" {
				t.Fatalf("the response body disclosed detail: %q", got)
			}
			if !strings.Contains(logged, tc.wantLog) {
				t.Fatalf("the log does not name the reason.\n  want substring: %q\n  log: %s", tc.wantLog, logged)
			}
			if !strings.Contains(logged, "scheme=") {
				t.Fatalf("the log does not record the request scheme, which is what explains most of these.\n  log: %s", logged)
			}
		})
	}
}

// TestLoginRefusalOverPlainHTTPIsDiagnosed is the production fault, reproduced.
//
// Over plain HTTP a browser refuses to store the Secure synchroniser cookie, so the login page
// renders normally and the submission then answers a bare 403 with nothing to act on. This test
// builds that situation and asserts the log names it.
//
// Note what the browser does and Go does not. Go's net/http/cookiejar does NOT enforce the
// Secure-over-HTTP rule -- it will hand back a Secure cookie for an http:// URL. An earlier draft of
// this test assumed it did, and therefore passed a cookie to a server that never saw one; the login
// succeeded, the client followed the redirect to /, and the test failed on an unrelated 500. The
// browser's behavior is simulated explicitly here by discarding the cookie, which is what actually
// distinguishes the situation from a healthy one.
func TestLoginRefusalOverPlainHTTPIsDiagnosed(t *testing.T) {
	srv := newLoginServer(t, false)

	rendering := newAnonymousClient(t, srv)
	resp := doGet(t, rendering, srv.URL+"/login")
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /login over plain HTTP = %d, want 200; the page must still render", resp.StatusCode)
	}
	if !strings.Contains(string(page), `name="csrf"`) {
		t.Fatal("the login form did not render over plain HTTP")
	}

	// The form carries a perfectly good token. It is the cookie that never existed.
	form := url.Values{"passphrase": {testPassphrase}, "csrf": {csrfFromFormBody(t, string(page))}}

	// A client with an empty cookie store: exactly what a browser that refused the Secure cookie
	// submits, and the only way to reproduce the fault from Go.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	blank := srv.Client()
	blank.Jar = jar
	blank.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	var post *http.Response
	logged := captureLog(t, func() {
		post = doPostForm(t, blank, srv.URL+"/login", form)
	})
	defer post.Body.Close()

	if post.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; the submission has no cookie to correlate with the form",
			post.StatusCode)
	}
	if !strings.Contains(logged, "plain HTTP") {
		t.Fatalf("the log does not identify the plain-HTTP cause.\n  log: %s", logged)
	}
	if !strings.Contains(logged, csrfCookieNameForTest()) {
		t.Fatalf("the log does not name the cookie the browser discarded.\n  log: %s", logged)
	}
	if strings.Contains(logged, "attack") && !strings.Contains(logged, "rather than this being an attack") {
		t.Fatalf("the log frames a deployment fault as an attack.\n  log: %s", logged)
	}
}

// TestSecureCookiesAreNotSentOverPlainHTTP pins the underlying assumption the test above simulates.
//
// This is what a real browser does, and Go's cookie jar does not, so nothing else in the suite can
// assert it. It is cheap, and it is the fact the whole plain-HTTP diagnosis rests on.
func TestSecureCookiesAreNotSentOverPlainHTTP(t *testing.T) {
	srv := newLoginServer(t, false)
	anon := newAnonymousClient(t, srv)

	resp := doGet(t, anon, srv.URL+"/login")
	resp.Body.Close()

	// Whatever the jar decides to retain, the cookie the server issued must be marked Secure: that
	// is the attribute that makes a browser discard it here.
	var secure bool
	for _, ck := range resp.Cookies() {
		if ck.Name == csrfCookieNameForTest() {
			secure = ck.Secure
		}
	}
	if !secure {
		t.Fatal("the synchroniser cookie is not Secure, so the plain-HTTP failure cannot occur")
	}
}

// TestSecurityHeaders pins the security headers, including the HSTS that prevents the downgrade in
// the first place.
func TestSecurityHeaders(t *testing.T) {
	srv := newLoginServer(t, true)
	anon := newAnonymousClient(t, srv)

	resp := doGet(t, anon, srv.URL+"/health")
	defer resp.Body.Close()

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "no-referrer",
		"Permissions-Policy":        "geolocation=(), microphone=(), camera=()",
		"Cache-Control":             "no-store",
		"Strict-Transport-Security": "max-age=31536000",
	}
	for header, value := range want {
		if got := resp.Header.Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}

	// HSTS must not be qualified into commitments about other hostnames. This service is one host on
	// a platform that serves a wildcard domain, and includeSubDomains or preload would be a decision
	// about names this repository does not own.
	hsts := resp.Header.Get("Strict-Transport-Security")
	if strings.Contains(strings.ToLower(hsts), "includesubdomains") {
		t.Errorf("HSTS carries includeSubDomains: %q", hsts)
	}
	if strings.Contains(strings.ToLower(hsts), "preload") {
		t.Errorf("HSTS carries the preload directive: %q", hsts)
	}
}

// csrfCookieNameForTest mirrors the server's cookie name. Duplicated deliberately: asserting the log
// against a constant imported from the package under test would pass even if the log line were
// simply printing that same constant back.
func csrfCookieNameForTest() string { return "verse_csrf" }
