package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/database"
)

func TestV019LibraryFlowE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("VERSE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("set VERSE_E2E_DATABASE_URL to run v0.1.9 end-to-end tests")
	}

	prev := os.Getenv("DATABASE_URL")
	if err := os.Setenv("DATABASE_URL", dsn); err != nil {
		t.Fatalf("set DATABASE_URL: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("DATABASE_URL", prev)
	})

	if err := database.Connect(); err != nil {
		t.Fatalf("database connect: %v", err)
	}
	t.Cleanup(func() {
		if database.Pool != nil {
			database.Pool.Close()
			database.Pool = nil
		}
	})

	ctx := context.Background()
	if _, err := database.Pool.Exec(ctx, `TRUNCATE TABLE poems`); err != nil {
		t.Fatalf("truncate poems: %v", err)
	}

	srv := httptest.NewServer(newRouter())
	defer srv.Close()

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
	if !strings.Contains(body, "No poems found") {
		t.Fatalf("GET /poems after delete expected empty-state text, got: %q", body)
	}
}

func TestV019RouteMapExists(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("VERSE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("set VERSE_E2E_DATABASE_URL to run route map test")
	}

	prev := os.Getenv("DATABASE_URL")
	if err := os.Setenv("DATABASE_URL", dsn); err != nil {
		t.Fatalf("set DATABASE_URL: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("DATABASE_URL", prev)
	})

	if err := database.Connect(); err != nil {
		t.Fatalf("database connect: %v", err)
	}
	t.Cleanup(func() {
		if database.Pool != nil {
			database.Pool.Close()
			database.Pool = nil
		}
	})

	srv := httptest.NewServer(newRouter())
	defer srv.Close()

	checks := []string{"/", "/dashboard", "/editor", "/library", "/poems", "/caelum", "/prompt"}
	for _, path := range checks {
		status, _, _ := get(t, srv.URL+path, nil)
		if status >= 500 {
			t.Fatalf("GET %s returned server error status %d", path, status)
		}
	}
}

func TestV019SpatialNavigationAcrossScreensE2E(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("VERSE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("set VERSE_E2E_DATABASE_URL to run spatial navigation e2e test")
	}

	prev := os.Getenv("DATABASE_URL")
	if err := os.Setenv("DATABASE_URL", dsn); err != nil {
		t.Fatalf("set DATABASE_URL: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("DATABASE_URL", prev)
	})

	if err := database.Connect(); err != nil {
		t.Fatalf("database connect: %v", err)
	}
	t.Cleanup(func() {
		if database.Pool != nil {
			database.Pool.Close()
			database.Pool = nil
		}
	})

	ctx := context.Background()
	if _, err := database.Pool.Exec(ctx, `TRUNCATE TABLE poems`); err != nil {
		t.Fatalf("truncate poems: %v", err)
	}

	srv := httptest.NewServer(newRouter())
	defer srv.Close()

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

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
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

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
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
