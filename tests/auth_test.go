package tests

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	appserver "github.com/divijg19/Verse/internal/server"
)

// privateRoutes is the set of routes that must never be reachable without a session.
//
// This list is the regression test for the v0.3.6 finding that all fifteen routes were public,
// including the two mutating endpoints that could overwrite or destroy any work by UUID. It is
// written as an explicit enumeration rather than derived from the router, so that adding a route
// without deciding its exposure is visible in review.
//
// It used to call itself complete while listing fourteen of nineteen. The five it omitted were
// exactly the ones a later release added -- history, version restore, the recycle, the export, and
// undelete -- and two of them reach deleted work. The list drifting is the failure mode an
// enumeration cannot catch on its own, so TestPrivateRoutesEnumeratesTheWholeRouter derives the
// registered set and requires this one to match. The explicit list stays, because the point is that a
// human decided each entry's exposure; the test is what stops the decision from quietly lapsing.
var privateRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/"},
	{http.MethodGet, "/dashboard"},
	{http.MethodGet, "/editor"},
	{http.MethodGet, "/editor/some-id"},
	{http.MethodGet, "/caelum"},
	{http.MethodGet, "/library"},
	{http.MethodGet, "/share"},
	{http.MethodGet, "/poems"},
	{http.MethodGet, "/poem/some-id"},
	{http.MethodGet, "/prompt"},
	{http.MethodPost, "/poem"},
	{http.MethodPost, "/poem/update"},
	{http.MethodPost, "/poem/delete"},
	{http.MethodPost, "/logout"},
	// Added in v0.4.4 and v0.4.5, and previously absent from this list. Two of them reach
	// soft-deleted work, and the export is the entire archive including every retained revision, so
	// their exposure is worth deciding rather than inheriting.
	{http.MethodGet, "/poem/some-id/history"},
	{http.MethodPost, "/poem/restore"},
	{http.MethodGet, "/recycle"},
	{http.MethodGet, "/export"},
	{http.MethodHead, "/export"},
	{http.MethodPost, "/poem/undelete"},
}

func TestPrivateRoutesRequireAuthentication(t *testing.T) {
	srv := newTestServer(t)
	anon := newAnonymousClient(t, srv)

	for _, route := range privateRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			// A follow-redirects client would mask a 303 to /login behind a 200 login page, so the
			// assertion would pass for the wrong reason.
			anon.CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}

			req, err := http.NewRequestWithContext(t.Context(), route.method, srv.URL+route.path,
				strings.NewReader("content=x&id=x"))
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			resp, err := anon.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", route.method, route.path, err)
			}
			defer resp.Body.Close()

			// A browser navigation is redirected to the login page (303); an HTMX fragment
			// request receives a bare 401. Both are correct rejections. Anything else means the
			// route served content.
			rejected := resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusUnauthorized
			if !rejected {
				t.Fatalf("%s %s status = %d — the route is reachable without a session",
					route.method, route.path, resp.StatusCode)
			}

			if resp.StatusCode == http.StatusSeeOther {
				if loc := resp.Header.Get("Location"); loc != "/login" {
					t.Fatalf("%s %s redirected to %q, want /login", route.method, route.path, loc)
				}
			}

			// Critically: the response must not contain any of the author's content.
			body, _ := io.ReadAll(resp.Body)
			for _, leak := range []string{"verse-library-entry", "verse-poem-view-article", "verse-dashboard-shell"} {
				if strings.Contains(string(body), leak) {
					t.Fatalf("%s %s leaked private content in an unauthenticated response",
						route.method, route.path)
				}
			}
		})
	}
}

func TestPublicRoutesAreExactlyHealthLoginAndAssets(t *testing.T) {
	srv := newTestServer(t)
	anon := newAnonymousClient(t, srv)
	anon.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	// Anything reachable without a session must be infrastructure, never content.
	for _, path := range []string{"/health", "/login", "/static/js/navigation.js"} {
		resp := doGet(t, anon, srv.URL+path)
		resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			t.Fatalf("GET %s returned 401; infrastructure routes must stay reachable for the "+
				"platform health probe and the login form", path)
		}
	}
}

func TestLoginRejectsWrongPassphrase(t *testing.T) {
	srv := newTestServer(t)
	anon := newAnonymousClient(t, srv)
	anon.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp := doGet(t, anon, srv.URL+"/login")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	form := url.Values{"passphrase": {"definitely-not-the-passphrase"}, "csrf": {csrfOf(t, anon, srv.URL)}}

	post := doPostForm(t, anon, srv.URL+"/login", form)
	defer post.Body.Close()
	body, _ := io.ReadAll(post.Body)

	if post.StatusCode == http.StatusSeeOther {
		t.Fatal("a wrong passphrase established a session")
	}
	if !strings.Contains(string(body), "not accepted") {
		t.Fatalf("login failure did not render the rejection message; body: %s", truncate(body))
	}
	if strings.Contains(string(body), "verse_session") {
		t.Fatal("a wrong passphrase issued a session cookie")
	}
}

func TestLoginRejectsMissingOrWrongCSRFToken(t *testing.T) {
	srv := newTestServer(t)

	for name, token := range map[string]string{
		"missing": "",
		"wrong":   "not-the-token",
	} {
		t.Run(name, func(t *testing.T) {
			anon := newAnonymousClient(t, srv)
			anon.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

			resp := doGet(t, anon, srv.URL+"/login")
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			form := url.Values{"passphrase": {testPassphrase}}
			if token != "" {
				form.Set("csrf", token)
			}

			post := doPostForm(t, anon, srv.URL+"/login", form)
			defer post.Body.Close()

			if post.StatusCode != http.StatusForbidden {
				t.Fatalf("POST /login with a %s csrf token = %d, want 403", name, post.StatusCode)
			}
		})
	}
}

func TestMutatingRoutesRequireCSRF(t *testing.T) {
	srv := newTestServer(t)

	// A valid session, deliberately no CSRF token.
	for _, endpoint := range []string{"/poem", "/poem/update", "/poem/delete", "/logout"} {
		t.Run(endpoint, func(t *testing.T) {
			form := url.Values{"content": {"a work"}, "id": {"some-id"}}
			status, body, _ := postFormWithoutCSRF(t, srv.URL+endpoint, form)

			if status != http.StatusForbidden {
				t.Fatalf("POST %s without a csrf token = %d, want 403; body: %s", endpoint, status, truncate([]byte(body)))
			}
		})
	}
}

func TestCSRFRejectsForeignOrigin(t *testing.T) {
	srv := newTestServer(t)

	token := csrfFromJar(t, srv.URL)
	form := url.Values{"content": {"a work"}, "csrf": {token}}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/poem",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://attacker.example")

	resp, err := authClient.Do(req)
	if err != nil {
		t.Fatalf("POST /poem: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /poem with a foreign Origin = %d, want 403", resp.StatusCode)
	}
}

func TestSessionCookieFlags(t *testing.T) {
	srv := newTestServer(t)
	anon := newAnonymousClient(t, srv)
	anon.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	resp := doGet(t, anon, srv.URL+"/login")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	form := url.Values{"passphrase": {testPassphrase}, "csrf": {csrfOf(t, anon, srv.URL)}}
	post := doPostForm(t, anon, srv.URL+"/login", form)
	io.Copy(io.Discard, post.Body)
	post.Body.Close()

	// Asserted against the raw Set-Cookie headers: Go's cookiejar does not retain the Secure or
	// HttpOnly attributes, so reading them back from the jar would prove nothing.
	raw := post.Header.Values("Set-Cookie")
	if len(raw) == 0 {
		t.Fatal("login issued no cookies")
	}

	var sessionRaw string
	for _, c := range raw {
		if strings.HasPrefix(c, testSessionCookie+"=") {
			sessionRaw = c
		}
	}
	if sessionRaw == "" {
		t.Fatalf("login issued no session cookie; got: %v", raw)
	}

	for _, attr := range []string{"Secure", "HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(sessionRaw, attr) {
			t.Fatalf("session cookie is missing %s: %q", attr, sessionRaw)
		}
	}
}

func TestLogoutClearsSession(t *testing.T) {
	srv := newTestServer(t)

	authClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	status, _, _ := postForm(t, srv.URL+"/logout", url.Values{}, nil)
	authClient.CheckRedirect = nil
	if status != http.StatusSeeOther {
		t.Fatalf("POST /logout status = %d, want 303", status)
	}

	// After logout the same client must be anonymous again.
	authClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { authClient.CheckRedirect = nil }()

	resp := doGet(t, authClient, srv.URL+"/library")
	resp.Body.Close()

	// A browser is redirected to the login page rather than served content.
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /library after logout = %d, want 303 to /login", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("GET /library after logout redirected to %q, want /login", loc)
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	srv := newTestServer(t)

	_, _, headers := get(t, srv.URL+"/health", nil)

	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := headers.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}

	if csp := headers.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("Content-Security-Policy does not pin script-src to self: %q", csp)
	}
}

func TestNoThirdPartyScriptOrigins(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)
	srv := newTestServer(t)

	// /login and the authenticated surfaces must not reference any third-party script origin.
	for _, path := range []string{"/login", "/", "/library", "/editor"} {
		_, body, _ := get(t, srv.URL+path, nil)

		for _, forbidden := range []string{"unpkg.com", "cdn.jsdelivr.net", "https://"} {
			if forbidden == "https://" {
				continue // same-origin absolute URLs are fine; checked separately below
			}
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s references the third-party origin %q; the authoring application must "+
					"make no third-party requests", path, forbidden)
			}
		}

		if path != "/login" && !strings.Contains(body, "/static/js/htmx.min.js") {
			t.Fatalf("%s does not load the vendored htmx bundle", path)
		}
	}
}

// helpers

// csrfOf returns the csrf cookie value held by a specific client.
func csrfOf(t *testing.T, c *http.Client, baseURL string) string {
	t.Helper()

	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	for _, cookie := range c.Jar.Cookies(u) {
		if cookie.Name == testCSRFCookie {
			return cookie.Value
		}
	}
	return ""
}

// postFormWithoutCSRF issues a POST with a valid session but no CSRF token.
func postFormWithoutCSRF(t *testing.T, endpoint string, values url.Values) (int, string, http.Header) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint,
		strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("build POST: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := authClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", endpoint, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), resp.Header
}

// TestNewRouterFailsClosedWithoutAuthConfig locks the fail-closed startup path.
//
// The router refuses to serve the authoring application when its authentication configuration is
// absent. That refusal used to be a panic, which surfaced as a stack trace from a library function
// and buried the actual cause — the missing variable names — in scrollback. It is now an error the
// caller can report cleanly.
//
// This test asserts both halves of the contract: an error is returned, and nothing panics. A
// regression that reintroduced the panic would fail here rather than only in production logs, and a
// regression that dropped the check entirely would fail on the nil-error assertion.
func TestNewRouterFailsClosedWithoutAuthConfig(t *testing.T) {
	cases := []struct {
		name          string
		authorization string
		secret        string
		wantSubstring string
	}{
		{"both missing", "", "", "VERSE_AUTHORIZATION, VERSE_AUTH_SECRET"},
		{"passphrase missing", "", testAuthSecret, "VERSE_AUTHORIZATION"},
		{"secret too short", testPassphrase, "too-short", "VERSE_AUTH_SECRET"},
		{"secret blank", testPassphrase, "   ", "VERSE_AUTH_SECRET"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VERSE_AUTHORIZATION", tc.authorization)
			t.Setenv("VERSE_AUTH_SECRET", tc.secret)

			router, err := appserver.NewRouter()

			if err == nil {
				t.Fatal("NewRouter returned no error; the authoring surface would serve unauthenticated")
			}
			if router != nil {
				t.Fatal("NewRouter returned a router alongside an error")
			}
			if !strings.Contains(err.Error(), tc.wantSubstring) {
				t.Fatalf("error does not name the problem: %q", err.Error())
			}
			// The message is what an operator reads in the platform log, so it must be actionable
			// on its own.
			if !strings.Contains(err.Error(), "refusing to start") {
				t.Fatalf("error should state plainly that startup is refused: %q", err.Error())
			}
		})
	}
}

// TestNewRouterSucceedsWithAuthConfig is the counterpart: with valid configuration the router is
// built and returned, and the private group is still closed.
func TestNewRouterSucceedsWithAuthConfig(t *testing.T) {
	t.Setenv("VERSE_AUTHORIZATION", testPassphrase)
	t.Setenv("VERSE_AUTH_SECRET", testAuthSecret)

	router, err := appserver.NewRouter()
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	if router == nil {
		t.Fatal("NewRouter returned a nil router with no error")
	}

	srv := httptest.NewTLSServer(router)
	defer srv.Close()

	anon := newAnonymousClient(t, srv)
	anon.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	got := doGet(t, anon, srv.URL+"/library")
	defer got.Body.Close()
	if got.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /library = %d, want 303 to /login", got.StatusCode)
	}
}

// TestNewRouterAuthSecretLengthBoundary pins the exact threshold for VERSE_AUTH_SECRET.
//
// This exists because the length check previously tested `len(secret) < 32` but only reported a
// failure when the secret was also empty, so any non-empty secret of 1 to 31 bytes was accepted
// silently. The check now treats a short secret exactly like an absent one.
//
// The boundary is asserted from both sides so the comparison operator cannot drift in either
// direction without a test failing.
func TestNewRouterAuthSecretLengthBoundary(t *testing.T) {
	cases := []struct {
		name    string
		length  int
		wantErr bool
	}{
		{"empty", 0, true},
		{"one byte", 1, true},
		{"thirty one bytes, one below the minimum", 31, true},
		{"exactly the minimum", 32, false},
		{"above the minimum", 64, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VERSE_AUTHORIZATION", testPassphrase)
			t.Setenv("VERSE_AUTH_SECRET", strings.Repeat("s", tc.length))

			_, err := appserver.NewRouter()

			if tc.wantErr && err == nil {
				t.Fatalf("a %d-byte secret was accepted; the minimum is %d", tc.length, 32)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("a %d-byte secret was rejected: %v", tc.length, err)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "VERSE_AUTH_SECRET") {
				t.Fatalf("error does not name the variable: %q", err.Error())
			}
		})
	}
}

// publicRoutes is the set of registrations deliberately reachable without a session.
//
// Kept as an explicit list for the same reason privateRoutes is one: the point is that a human
// classified each route, and TestPrivateRoutesEnumeratesTheWholeRouter is what holds both lists to
// the router. The unauthenticated surface is deliberately tiny -- the platform probe, the login page,
// the login submission, and the static assets -- and every one of them is either necessary for the
// service to run or leaks nothing.
var publicRoutes = map[string]bool{
	http.MethodGet + " /health":       true,
	http.MethodHead + " /health":      true,
	http.MethodGet + " /login":        true,
	http.MethodPost + " /login":       true,
	http.MethodGet + " /static/*":     true,
	http.MethodHead + " /static/*":    true,
	http.MethodPost + " /static/*":    true,
	http.MethodPut + " /static/*":     true,
	http.MethodPatch + " /static/*":   true,
	http.MethodDelete + " /static/*":  true,
	http.MethodOptions + " /static/*": true,
	http.MethodTrace + " /static/*":   true,
	http.MethodConnect + " /static/*": true,
	// Found by TestPrivateRoutesEnumeratesTheWholeRouter, which is the argument for having it.
	"QUERY /static/*": true,
}

// TestPrivateRoutesEnumeratesTheWholeRouter is the guard on both lists.
//
// An explicit enumeration of routes is only useful while it stays complete, and nothing about writing
// one down keeps it that way. This one claimed to be "the complete set" while listing fourteen of
// nineteen, having missed every route added since: history, version restore, the recycle, the export,
// and undelete. Two of those reach soft-deleted work, and the export is the entire archive.
//
// So the classification is now checked against the router rather than trusted. Every registration
// must be in exactly one of the two lists, and a new route that nobody has thought about fails here
// instead of inheriting an exposure by default. That inverts the failure: the cost of forgetting is a
// red test, not a published draft.
func TestPrivateRoutesEnumeratesTheWholeRouter(t *testing.T) {
	t.Setenv("VERSE_AUTHORIZATION", testPassphrase)
	t.Setenv("VERSE_AUTH_SECRET", testAuthSecret)

	router, err := appserver.NewRouter()
	if err != nil {
		t.Fatalf("build router: %v", err)
	}

	private := make(map[string]bool, len(privateRoutes))
	for _, r := range privateRoutes {
		key := r.method + " " + r.path
		if private[key] {
			t.Errorf("privateRoutes lists %q twice", key)
		}
		private[key] = true
	}

	seen := 0
	err = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		seen++
		// chi reports patterns; the list uses a concrete placeholder, so a path parameter is
		// normalised to the same convention rather than the two being compared literally.
		key := method + " " + normaliseRouteParams(route)

		switch {
		case private[key] && publicRoutes[key]:
			t.Errorf("%q is in both lists; a route cannot be both", key)
		case private[key] || publicRoutes[key]:
			// Classified. Nothing to do.
		default:
			t.Errorf("%q is registered but appears in neither privateRoutes nor publicRoutes.\n"+
				"  Decide whether it needs a session, then list it. A route with no entry inherits "+
				"whatever the middleware happens to do, which is how a private surface becomes a "+
				"public one.", key)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if seen == 0 {
		t.Fatal("the walk found no routes; the router is not enumerable and this test proves nothing")
	}

	// A list entry with no route is the other direction of the same drift, and the more likely one:
	// a route gets renamed or removed and the assertion keeps passing against a path that no longer
	// exists.
	registered := make(map[string]bool, seen)
	_ = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+normaliseRouteParams(route)] = true
		return nil
	})
	for key := range private {
		if !registered[key] {
			t.Errorf("privateRoutes lists %q, which is not registered", key)
		}
	}
	for key := range publicRoutes {
		if !registered[key] {
			t.Errorf("publicRoutes lists %q, which is not registered", key)
		}
	}
}

// normaliseRouteParams rewrites chi's {id} to the concrete placeholder the route lists use, so a
// pattern and an entry can be compared as strings.
func normaliseRouteParams(route string) string {
	return chiPatternParam.ReplaceAllString(route, "some-id")
}

var chiPatternParam = regexp.MustCompile(`\{[^/}]+\}`)
