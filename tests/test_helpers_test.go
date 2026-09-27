package tests

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/migrate"
	appserver "github.com/divijg19/Verse/internal/server"
	"github.com/divijg19/Verse/internal/testsupport"
	"github.com/google/uuid"
)

// Test credentials.
//
// The router refuses to start without authentication configured, by design: serving the authoring
// application unauthenticated is never correct. Tests therefore set both variables, and the
// passphrase is a fixed literal so the suite carries no real secret.
const (
	testPassphrase    = "verse-test-passphrase"
	testAuthSecret    = "verse-test-auth-secret-0123456789abcdef"
	testCSRFCookie    = "verse_csrf"
	testSessionCookie = "verse_session"
)

// authClient is the shared client for the suite. It carries a cookie jar and automatically attaches
// the CSRF synchroniser token to mutating requests, mirroring what a browser does after login.
var authClient *http.Client

// requireTestDSN returns the DSN for database-backed tests, or skips.
//
// The gate lives in internal/testsupport so that this package and cmd/server enforce the same rules
// from one implementation. See DisposableDSN for why both gates are required.
func requireTestDSN(t *testing.T) string {
	t.Helper()

	dsn, reason := testsupport.DisposableDSN()
	if reason != "" {
		t.Skip(reason)
	}

	return dsn
}

func connectTestDB(t *testing.T) {
	t.Helper()

	dsn := requireTestDSN(t)
	t.Setenv("DATABASE_URL", dsn)

	if database.Pool != nil {
		database.Pool.Close()
		database.Pool = nil
	}

	if err := database.Connect(); err != nil {
		t.Fatalf("database connect failed: %v", err)
	}
	if _, err := migrate.Run(context.Background(), database.Pool); err != nil {
		t.Fatalf("database migrate: %v", err)
	}

	t.Cleanup(func() {
		if database.Pool != nil {
			database.Pool.Close()
			database.Pool = nil
		}
	})
}

// truncatePoems empties the poems table.
//
// This is one of only two places in the repository that may issue a TRUNCATE; the other is
// truncateE2EPoems in cmd/server. Both re-check the gate rather than trusting the caller, so a test
// that reaches one through an unusual path still cannot destroy a database that was never opted in.
// `git grep TRUNCATE` should return exactly two executable lines, and both should sit inside a
// guard that calls testsupport.DisposableDSN.
func truncatePoems(t *testing.T) {
	t.Helper()

	// Re-evaluated per call, not cached: a test that changed the environment mid-run is refused.
	if _, reason := testsupport.DisposableDSN(); reason != "" {
		t.Skip(reason)
	}

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	if _, err := database.Pool.Exec(context.Background(), `TRUNCATE TABLE poems`); err != nil {
		t.Fatalf("truncate poems failed: %v", err)
	}
}

// newTestServer starts the application and authenticates the shared client.
//
// Authentication is performed for real, over HTTP, through the same /login flow the author uses.
// Nothing is stubbed: a test that bypasses login would not exercise the security boundary it is
// meant to protect.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}

	if filepath.Base(wd) == "tests" {
		if err := os.Chdir(".."); err != nil {
			t.Fatalf("chdir to repo root failed: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Chdir(wd)
		})
	}

	t.Setenv("VERSE_AUTHORIZATION", testPassphrase)
	t.Setenv("VERSE_AUTH_SECRET", testAuthSecret)

	// A TLS server, deliberately. Session cookies are issued with the Secure flag, which a browser
	// and Go's cookie jar both refuse to store over plain HTTP. Testing against http:// would
	// therefore never exercise the real production cookie configuration.
	router, err := appserver.NewRouter()
	if err != nil {
		t.Fatalf("build router: %v", err)
	}

	srv := httptest.NewTLSServer(router)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}

	// srv.Client() trusts the test server's certificate.
	authClient = srv.Client()
	authClient.Jar = jar
	t.Cleanup(func() { authClient = nil })

	login(t, srv.URL)

	return srv
}

// newAnonymousClient returns a TLS-trusting client with no credentials, for exercising the
// authentication boundary from the outside.
func newAnonymousClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}

	c := srv.Client()
	c.Jar = jar
	return c
}

// login performs the real login flow and fails the test if it does not succeed.
func login(t *testing.T, baseURL string) {
	t.Helper()

	// GET /login to obtain the CSRF token, which is delivered in a cookie and echoed in the form.
	resp := doGet(t, authClient, baseURL+"/login")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200", resp.StatusCode)
	}

	csrf := csrfFromJar(t, baseURL)
	if csrf == "" {
		t.Fatalf("GET /login did not set a csrf cookie; body: %s", truncate(body))
	}
	if !strings.Contains(string(body), `name="csrf"`) {
		t.Fatalf("login form missing csrf field; body: %s", truncate(body))
	}

	// Do not follow the redirect: the assertion is that login itself answers 303, not that some
	// later page happens to answer 200.
	authClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	form := url.Values{"passphrase": {testPassphrase}, "csrf": {csrf}}
	post := doPostForm(t, authClient, baseURL+"/login", form)
	post.Body.Close()
	authClient.CheckRedirect = nil

	if post.StatusCode != http.StatusSeeOther {
		body, _ := io.ReadAll(post.Body)
		t.Fatalf("POST /login status = %d, want 303; body: %s", post.StatusCode, truncate(body))
	}

	if sessionFromJar(t, baseURL) == "" {
		t.Fatal("POST /login did not establish a session cookie")
	}
}

// csrfFromJar returns the CSRF token the server issued for the current session.
func csrfFromJar(t *testing.T, baseURL string) string {
	t.Helper()

	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	for _, c := range authClient.Jar.Cookies(u) {
		if c.Name == testCSRFCookie {
			return c.Value
		}
	}
	return ""
}

// sessionFromJar returns the session cookie value, or "" when absent.
func sessionFromJar(t *testing.T, baseURL string) string {
	t.Helper()

	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	for _, c := range authClient.Jar.Cookies(u) {
		if c.Name == testSessionCookie {
			return c.Value
		}
	}
	return ""
}

func truncate(b []byte) string {
	if len(b) > 400 {
		return string(b[:400]) + "..."
	}
	return string(b)
}

// doGet issues a context-bound GET.
//
// Every request in this suite is bound to t.Context(), which is canceled when the test finishes.
// A hung request therefore cannot outlive its test or leak into the next one, which matters
// because the suite runs serially against a shared database.
func doGet(t *testing.T, c *http.Client, endpoint string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", endpoint, err)
	}
	return resp
}

// doPostForm issues a context-bound form POST.
func doPostForm(t *testing.T, c *http.Client, endpoint string, form url.Values) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", endpoint, err)
	}
	return resp
}

func get(t *testing.T, endpoint string, headers map[string]string) (int, string, http.Header) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("create GET request failed: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := authClient.Do(req)
	if err != nil {
		t.Fatalf("execute GET request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET response failed: %v", err)
	}

	return resp.StatusCode, string(body), resp.Header
}

func postForm(t *testing.T, endpoint string, values url.Values, headers map[string]string) (int, string, http.Header) {
	t.Helper()

	// Attach the session-bound CSRF token, exactly as the rendered form does. Without it the
	// mutating endpoints correctly reject the request.
	if values.Get("csrf") == "" {
		if token := csrfFromJar(t, endpointOrigin(endpoint)); token != "" {
			values = cloneValues(values)
			values.Set("csrf", token)
		}
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("create POST request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := authClient.Do(req)
	if err != nil {
		t.Fatalf("execute POST request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST response failed: %v", err)
	}

	return resp.StatusCode, string(body), resp.Header
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}
	return out
}

func endpointOrigin(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	return u.Scheme + "://" + u.Host
}

func insertPoem(t *testing.T, content string) string {
	t.Helper()

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	id := uuid.NewString()
	if _, err := database.Pool.Exec(context.Background(), `INSERT INTO poems (id, content) VALUES ($1, $2)`, id, content); err != nil {
		t.Fatalf("insert poem failed: %v", err)
	}

	return id
}

func insertPoemAt(t *testing.T, content string, createdAt time.Time) string {
	t.Helper()

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	id := uuid.NewString()
	if _, err := database.Pool.Exec(context.Background(), `INSERT INTO poems (id, content, created_at) VALUES ($1, $2, $3)`, id, content, createdAt.UTC()); err != nil {
		t.Fatalf("insert timed poem failed: %v", err)
	}

	return id
}

func markPoemDeleted(t *testing.T, id string) {
	t.Helper()

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	if _, err := database.Pool.Exec(context.Background(), `UPDATE poems SET deleted_at = NOW() WHERE id = $1`, id); err != nil {
		t.Fatalf("soft delete poem failed: %v", err)
	}
}

func poemContentByID(t *testing.T, id string) string {
	t.Helper()

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	var content string
	if err := database.Pool.QueryRow(context.Background(), `SELECT content FROM poems WHERE id = $1`, id).Scan(&content); err != nil {
		t.Fatalf("query poem content failed: %v", err)
	}

	return content
}

func poemDeletedByID(t *testing.T, id string) bool {
	t.Helper()

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}

	var deleted bool
	if err := database.Pool.QueryRow(context.Background(), `SELECT deleted_at IS NOT NULL FROM poems WHERE id = $1`, id).Scan(&deleted); err != nil {
		t.Fatalf("query poem deleted status failed: %v", err)
	}

	return deleted
}
