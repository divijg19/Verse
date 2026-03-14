package tests

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/database"
)

func TestIntegrationPoemFlow(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	defer srv.Close()

	original := "Lantern across dark water\nDust in late sunlight"
	status, body, _ := postForm(t, srv.URL+"/poem", url.Values{"content": {original}}, nil)
	if status != 200 {
		t.Fatalf("POST /poem status = %d, want 200", status)
	}
	if !strings.Contains(body, "Bloom recorded") {
		t.Fatalf("POST /poem missing success text: %q", body)
	}

	var id string
	if err := database.Pool.QueryRow(context.Background(), `SELECT id FROM poems WHERE content = $1 ORDER BY created_at DESC LIMIT 1`, original).Scan(&id); err != nil {
		t.Fatalf("lookup created poem id failed: %v", err)
	}

	status, body, _ = get(t, srv.URL+"/poem/"+id, nil)
	if status != 200 {
		t.Fatalf("GET /poem/{id} status = %d, want 200", status)
	}
	if !strings.Contains(body, "Dust in late sunlight") {
		t.Fatalf("GET /poem/{id} missing original content: %q", body)
	}

	updated := "Aurora in borrowed glass"
	status, body, _ = postForm(t, srv.URL+"/poem/update", url.Values{
		"id":      {id},
		"content": {updated},
	}, nil)
	if status != 200 {
		t.Fatalf("POST /poem/update status = %d, want 200", status)
	}
	if !strings.Contains(body, "Bloom updated") {
		t.Fatalf("POST /poem/update missing success text: %q", body)
	}

	status, body, _ = get(t, srv.URL+"/poem/"+id, nil)
	if status != 200 {
		t.Fatalf("GET /poem/{id} after update status = %d, want 200", status)
	}
	if !strings.Contains(body, updated) {
		t.Fatalf("GET /poem/{id} after update missing updated content: %q", body)
	}

	status, _, headers := postForm(t, srv.URL+"/poem/delete", url.Values{"id": {id}}, map[string]string{"HX-Request": "true"})
	if status != 200 {
		t.Fatalf("POST /poem/delete status = %d, want 200", status)
	}
	if headers.Get("HX-Redirect") != "/library" {
		t.Fatalf("POST /poem/delete missing HX-Redirect /library")
	}

	status, body, _ = get(t, srv.URL+"/poems?q=Aurora", map[string]string{"HX-Request": "true"})
	if status != 200 {
		t.Fatalf("GET /poems after delete status = %d, want 200", status)
	}
	if strings.Contains(body, updated) {
		t.Fatalf("deleted poem still visible in /poems results: %q", body)
	}
}
