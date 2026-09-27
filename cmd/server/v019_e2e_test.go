package main

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/migrate"
	"github.com/divijg19/Verse/internal/testsupport"
)

// requireDisposableDSN points the application at a verified-disposable test database, or skips.
//
// The gate itself lives in internal/testsupport so this package and the tests package cannot drift
// apart. Only the DATABASE_URL swap is local: the end-to-end scenarios drive the real application
// startup path, which reads DATABASE_URL. The swap is reverted by t.Cleanup, including unsetting the
// variable when it was not set to begin with, so a scenario never leaves the process pointed at a
// test database.
// e2eSchema is this package's private schema in the test database.
//
// The end-to-end scenarios truncate poems, and they used to do it in the default schema alongside the
// tests package. That is the shared state -p 1 existed to serialize. A schema per package removes
// the sharing, so the constraint is a property of the setup rather than a flag someone has to
// remember.
const e2eSchema = "verse_t_cmdserver"

func requireDisposableDSN(t *testing.T) {
	t.Helper()

	dsn, reason := testsupport.DisposableDSN()
	if reason != "" {
		t.Skip(reason)
	}

	scoped, err := testsupport.EnsurePackageSchema(t.Context(), dsn, e2eSchema)
	if err != nil {
		t.Fatalf("package schema: %v", err)
	}

	prev, had := os.LookupEnv("DATABASE_URL")
	if err := os.Setenv("DATABASE_URL", scoped); err != nil {
		t.Fatalf("set DATABASE_URL: %v", err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("DATABASE_URL", prev)
			return
		}
		_ = os.Unsetenv("DATABASE_URL")
	})
}

// truncateE2EPoems empties the poems table for a scenario.
//
// Like its counterpart in the tests package, the gate is re-checked here rather than trusted from
// the caller, so the point of destruction is the point of enforcement.
func truncateE2EPoems(t *testing.T) {
	t.Helper()

	if _, reason := testsupport.DisposableDSN(); reason != "" {
		t.Skip(reason)
	}

	if database.Pool == nil {
		t.Fatalf("database pool is nil")
	}
	if _, err := database.Pool.Exec(context.Background(), `TRUNCATE TABLE poems`); err != nil {
		t.Fatalf("truncate poems: %v", err)
	}
}

// e2eAuth performs the real login flow and installs a cookie-carrying client as e2eClient.
// Authentication is not stubbed: this suite exists to exercise the router, and the router's
// defining property in v0.3.7 is that it refuses to serve anything without a session.
func e2eAuth(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}

	c := srv.Client()
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	e2eBaseURL = srv.URL

	// Fetch the login form to obtain the CSRF token.
	resp := e2eDo(t, c, http.MethodGet, srv.URL+"/login", nil)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	form := url.Values{"passphrase": {e2ePassphrase}}
	u, _ := url.Parse(srv.URL)
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == "verse_csrf" {
			form.Set("csrf", cookie.Value)
		}
	}
	if form.Get("csrf") == "" {
		t.Fatal("login form did not issue a csrf cookie")
	}

	post := e2eDo(t, c, http.MethodPost, srv.URL+"/login", form)
	io.Copy(io.Discard, post.Body)
	post.Body.Close()

	if post.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login status = %d, want 303", post.StatusCode)
	}

	// Restore default redirect following for the rest of the flow.
	c.CheckRedirect = nil

	// e2eClient is consumed by the package-level get/postForm helpers.
	e2eClient = c
	return c
}

const (
	e2ePassphrase = "verse-e2e-passphrase"
	e2eAuthSecret = "verse-e2e-auth-secret-0123456789abcd"
)

// e2eDo issues a context-bound request. t.Context() is canceled when the test finishes, so a hung
// request cannot outlive its test.
func e2eDo(t *testing.T, c *http.Client, method, endpoint string, form url.Values) *http.Response {
	t.Helper()

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(t.Context(), method, endpoint, body)
	if err != nil {
		t.Fatalf("create %s request: %v", method, err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	return resp
}

// e2eClient is the authenticated client used by the package-level request helpers.
var (
	e2eClient  *http.Client
	e2eBaseURL string
)

// newE2EServer starts the application on a TLS listener and authenticates e2eClient.
func newE2EServer(t *testing.T) *httptest.Server {
	t.Helper()

	t.Setenv("VERSE_AUTHORIZATION", e2ePassphrase)
	t.Setenv("VERSE_AUTH_SECRET", e2eAuthSecret)

	router, err := newRouter()
	if err != nil {
		t.Fatalf("build router: %v", err)
	}

	srv := httptest.NewTLSServer(router)
	t.Cleanup(srv.Close)

	e2eAuth(t, srv)
	return srv
}

func TestV019LibraryFlowE2E(t *testing.T) {
	requireDisposableDSN(t)

	if err := database.Connect(); err != nil {
		t.Fatalf("database connect: %v", err)
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

	ctx := context.Background()
	truncateE2EPoems(t)

	srv := newE2EServer(t)

	poem := "Lantern across dark water\nDust in late sunlight"
	status, body, _ := postForm(t, srv.URL+"/poem", url.Values{"content": {poem}}, nil)
	if status != http.StatusOK {
		t.Fatalf("POST /poem status = %d, want 200", status)
	}
	if !strings.Contains(body, "Bloom recorded") {
		t.Fatalf("POST /poem body missing success text: %q", body)
	}

	var poemID string
	if err := database.Pool.QueryRow(ctx, `SELECT id FROM poems WHERE content = $1 ORDER BY created_at DESC LIMIT 1`, poem).Scan(&poemID); err != nil {
		t.Fatalf("lookup poem id: %v", err)
	}

	status, body, _ = get(t, srv.URL+"/library", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /library status = %d, want 200", status)
	}
	if !strings.Contains(body, "Library") || !strings.Contains(body, "Lantern across dark water") {
		t.Fatalf("GET /library body missing expected content: %q", body)
	}

	status, body, _ = get(t, srv.URL+"/poems?q=Lantern", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /poems status = %d, want 200", status)
	}
	if !strings.Contains(body, "Lantern across dark water") {
		t.Fatalf("GET /poems search did not include poem title: %q", body)
	}

	status, body, _ = get(t, srv.URL+"/poem/"+poemID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /poem/{id} status = %d, want 200", status)
	}
	if !strings.Contains(body, "Dust in late sunlight") {
		t.Fatalf("GET /poem/{id} missing poem content: %q", body)
	}

	status, body, _ = get(t, srv.URL+"/editor/"+poemID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /editor/{id} status = %d, want 200", status)
	}
	if !strings.Contains(body, "name=\"id\" value=\""+poemID+"\"") {
		t.Fatalf("GET /editor/{id} missing hidden id field: %q", body)
	}

	updated := "Aurora in borrowed glass"
	status, body, _ = postForm(t, srv.URL+"/poem/update", url.Values{
		"id":      {poemID},
		"content": {updated},
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("POST /poem/update status = %d, want 200", status)
	}
	if !strings.Contains(body, "Bloom updated") {
		t.Fatalf("POST /poem/update missing success text: %q", body)
	}

	status, body, _ = get(t, srv.URL+"/poem/"+poemID, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /poem/{id} after update status = %d, want 200", status)
	}
	if !strings.Contains(body, updated) {
		t.Fatalf("GET /poem/{id} after update missing updated content: %q", body)
	}

	headers := map[string]string{"HX-Request": "true"}
	status, _, respHeaders := postForm(t, srv.URL+"/poem/delete", url.Values{"id": {poemID}}, headers)
	if status != http.StatusOK {
		t.Fatalf("POST /poem/delete status = %d, want 200", status)
	}
	if got := respHeaders.Get("HX-Redirect"); got != "/library" {
		t.Fatalf("POST /poem/delete HX-Redirect = %q, want /library", got)
	}

	status, body, _ = get(t, srv.URL+"/poems?q=Aurora", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /poems after delete status = %d, want 200", status)
	}
	if strings.Contains(body, updated) {
		t.Fatalf("deleted poem still appears in search results: %q", body)
	}
	// The library copy was reworded after this assertion was written, and the suite had never run.
	// Assert the current, intentional wording instead of the superseded string.
	if !strings.Contains(body, "No poems match this search.") {
		t.Fatalf("GET /poems after delete expected empty-state text, got: %q", body)
	}
}

func TestV019RouteMapExists(t *testing.T) {
	requireDisposableDSN(t)

	if err := database.Connect(); err != nil {
		t.Fatalf("database connect: %v", err)
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

	srv := newE2EServer(t)

	checks := []string{"/", "/dashboard", "/editor", "/library", "/poems", "/caelum", "/prompt"}
	for _, path := range checks {
		status, _, _ := get(t, srv.URL+path, nil)
		if status >= 500 {
			t.Fatalf("GET %s returned server error status %d", path, status)
		}
	}
}

func TestV019SpatialNavigationAcrossScreensE2E(t *testing.T) {
	requireDisposableDSN(t)

	if err := database.Connect(); err != nil {
		t.Fatalf("database connect: %v", err)
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

	ctx := context.Background()
	truncateE2EPoems(t)

	srv := newE2EServer(t)

	status, _, _ := postForm(t, srv.URL+"/poem", url.Values{"content": {"Crossing from screen to screen"}}, nil)
	if status != http.StatusOK {
		t.Fatalf("seed poem via POST /poem status = %d, want 200", status)
	}

	var poemID string
	if err := database.Pool.QueryRow(ctx, `SELECT id FROM poems ORDER BY created_at DESC LIMIT 1`).Scan(&poemID); err != nil {
		t.Fatalf("lookup seeded poem id: %v", err)
	}

	type navExpectation struct {
		path   string
		top    string
		left   string
		right  string
		bottom string
	}

	checks := []navExpectation{
		{path: "/dashboard", top: "/caelum", left: "", right: "/editor", bottom: "/share"},
		{path: "/editor", top: "/caelum", left: "/dashboard", right: "/library", bottom: "/share"},
		{path: "/library", top: "/caelum", left: "/editor", right: "", bottom: "/share"},
		{path: "/caelum", top: "", left: "/dashboard", right: "/library", bottom: "/editor"},
		{path: "/share", top: "/editor", left: "/dashboard", right: "/library", bottom: ""},
	}

	headers := map[string]string{"HX-Request": "true"}
	for _, tc := range checks {
		status, body, _ := get(t, srv.URL+tc.path, headers)
		if status != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", tc.path, status)
		}

		assertNavSlotPath(t, body, "nav-top", tc.top)
		assertNavSlotPath(t, body, "nav-left", tc.left)
		assertNavSlotPath(t, body, "nav-right", tc.right)
		assertNavSlotPath(t, body, "nav-bottom", tc.bottom)
	}

	status, body, _ := get(t, srv.URL+"/poem/"+poemID, headers)
	if status != http.StatusOK {
		t.Fatalf("GET /poem/{id} status = %d, want 200", status)
	}
	if !strings.Contains(body, `hx-get="/library"`) {
		t.Fatalf("poem view missing navigation link back to /library")
	}
	if !strings.Contains(body, `hx-get="/editor/`+poemID+`"`) {
		t.Fatalf("poem view missing navigation link to /editor/{id}")
	}
}

func assertNavSlotPath(t *testing.T, body, slotID, expectedPath string) {
	t.Helper()

	marker := `id="` + slotID + `" hx-swap-oob="outerHTML"`
	idx := strings.Index(body, marker)
	if idx == -1 {
		t.Fatalf("missing nav slot marker %q in response body", marker)
	}

	fragment := body[idx:]
	end := strings.Index(fragment, "</div>")
	if end == -1 {
		t.Fatalf("missing closing div for nav slot %s", slotID)
	}
	slot := fragment[:end]

	if expectedPath == "" {
		if strings.Contains(slot, `hx-get="`) {
			t.Fatalf("nav slot %s unexpectedly had a link: %q", slotID, slot)
		}
		return
	}

	needle := `hx-get="` + expectedPath + `"`
	if !strings.Contains(slot, needle) {
		t.Fatalf("nav slot %s missing expected link %s in %q", slotID, needle, slot)
	}
}

func get(t *testing.T, endpoint string, headers map[string]string) (int, string, http.Header) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := e2eClient.Do(req)
	if err != nil {
		t.Fatalf("execute GET request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET response body: %v", err)
	}

	return resp.StatusCode, string(body), resp.Header
}

func postForm(t *testing.T, endpoint string, values url.Values, headers map[string]string) (int, string, http.Header) {
	t.Helper()

	// Attach the session-bound CSRF token, exactly as the rendered forms do. Without it the
	// mutating endpoints correctly reject the request.
	if values.Get("csrf") == "" {
		if u, err := url.Parse(e2eBaseURL); err == nil && e2eClient != nil {
			for _, cookie := range e2eClient.Jar.Cookies(u) {
				if cookie.Name == "verse_csrf" {
					clone := url.Values{}
					for k, v := range values {
						clone[k] = append([]string(nil), v...)
					}
					clone.Set("csrf", cookie.Value)
					values = clone
					break
				}
			}
		}
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := e2eClient.Do(req)
	if err != nil {
		t.Fatalf("execute POST request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read POST response body: %v", err)
	}

	return resp.StatusCode, string(body), resp.Header
}
