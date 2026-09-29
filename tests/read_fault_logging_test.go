package tests

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/database"
)

// TestAReadFaultIsLoggedOnEveryReadSurface is the behavioral half of the failure-logging rule.
//
// TestEveryInternalErrorResponseIsLogged proves structurally that every 500 goes through
// handlers.fail500. This proves that fail500 actually logs, on the two surfaces that were silent.
//
// The two were found separately and neither alone would have caught the other. The static guard
// would pass on a fail500 that logged nothing, and a behavioral test on /poem/{id} already existed
// and passed throughout, because that one path did log. The surfaces that did not log were the ones
// with no test that drove a fault through them at all -- so the fix for a database outage presenting
// as an empty page was verified only on the single path that already worked.
//
// A real fault rather than a fabricated error: the pool is closed underneath the request, which is
// the shape of an unreachable database, and it drives the same path an outage does.
func TestAReadFaultIsLoggedOnEveryReadSurface(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	// Seeded so an empty library cannot be mistaken for the expected outcome, and so a 404 would be
	// a claim the service demonstrably cannot support.
	insertPoem(t, "a work that exists")
	insertPoem(t, "another work, for the dashboard to summarize")

	srv := newTestServer(t)

	logged := captureLogs(t)

	live := database.Pool
	database.Pool = nil
	t.Cleanup(func() { database.Pool = live })

	// Every surface that reads the database and can therefore fail on it. The heatmap and search
	// fragments are included because they are the paths an author reaches *during* an incident --
	// clicking a month arrow, typing in the search box -- which is exactly when silence is least
	// acceptable.
	// Every surface that reads the database and can therefore fail on it. The search fragment and
	// the heatmap fragment are included because they are the paths an author reaches *during* an
	// incident -- typing in the search box, clicking a month arrow -- which is exactly when silence
	// is least acceptable.
	//
	// /share is deliberately absent: it renders a static component and touches no database, so it
	// cannot fault and there is nothing for it to log.
	// Every surface that reads the database and can therefore fail on it. The two fragments matter
	// as much as the pages: they are what an author reaches *during* an incident -- typing in the
	// search box, clicking a month arrow -- which is exactly when silence is least acceptable.
	//
	// /share is deliberately absent. It renders a static component and touches no database, so it
	// cannot fault and there is nothing for it to log.
	cases := []struct {
		name    string
		path    string
		headers map[string]string
	}{
		{name: "library", path: "/library"},
		{name: "dashboard", path: "/dashboard"},
		{name: "search fragment", path: "/poems?q=work"},
		{name: "recycle", path: "/recycle"},
		{name: "export", path: "/export"},
		{
			// The heatmap fragment is reached by clicking a month arrow, and only an HTMX request
			// with the right target selects that branch, so the headers are the route.
			name:    "heatmap fragment",
			path:    "/dashboard?month=2026-01",
			headers: map[string]string{"HX-Request": "true", "HX-Target": "heatmap"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logged.reset()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			status, body := resp.StatusCode, string(raw)

			if status != http.StatusInternalServerError {
				t.Fatalf("GET %s with the database unreachable = %d, want 500.\n  body: %s",
					tc.path, status, truncate([]byte(body)))
			}
			if strings.Contains(strings.ToLower(body), "database") {
				t.Errorf("the 500 body disclosed the fault: %q", body)
			}
			if !logged.contains("failed to") {
				t.Errorf("GET %s returned 500 and logged nothing.\n"+
					"  The rule this enforces is the one written after v0.4.4: a database outage must not\n"+
					"  present as an empty page with nothing in the log. Silence here is the failure --\n"+
					"  the log is the only place the fault is recorded. Captured: %q",
					tc.path, logged.text())
			}
		})
	}
}

// captureLogRecorder redirects the standard logger for the duration of a test and exposes what it
// collected.
//
// The standard logger rather than a project one, because the project uses the standard logger: that
// is a deliberate choice recorded in DECISIONS.md ("log is sufficient at current volume"), and it is
// the right sink to assert against, because testing a different one would prove nothing about the
// log an operator actually reads.
type captureLogRecorder struct {
	buf bytes.Buffer
}

// captureLogs redirects the standard logger's output and restores it on cleanup.
func captureLogs(t *testing.T) *captureLogRecorder {
	t.Helper()
	c := &captureLogRecorder{}
	previous := log.Writer()
	log.SetOutput(&c.buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	return c
}

func (c *captureLogRecorder) reset() { c.buf.Reset() }

func (c *captureLogRecorder) text() string { return strings.TrimSpace(c.buf.String()) }

func (c *captureLogRecorder) contains(substr string) bool {
	return strings.Contains(c.buf.String(), substr)
}
