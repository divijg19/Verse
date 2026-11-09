package tests

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/database"
)

func TestEditorSaveCreatesPoem(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	defer srv.Close()

	content := "A bell in snow"
	status, _, headers := postForm(t, srv.URL+"/poem", url.Values{"content": {content}}, map[string]string{"HX-Request": "true"})
	if status != 200 {
		t.Fatalf("POST /poem status = %d, want 200", status)
	}
	if redirect := headers.Get("HX-Redirect"); !strings.HasPrefix(redirect, "/editor/") {
		t.Fatalf("POST /poem HX-Redirect = %q, want /editor/{id}", redirect)
	}

	var count int
	if err := database.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM poems WHERE content = $1 AND deleted_at IS NULL`, content).Scan(&count); err != nil {
		t.Fatalf("count inserted poem failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("inserted poem count = %d, want 1", count)
	}
}

// TestSavingAgainAfterCreateUpdatesTheSameWork is the regression for the duplicate-create path.
//
// The create form carries no id, so a second save from the page that created the work used to post
// to /poem again and insert another row. The create response now hands off to /editor/{id}, whose
// forms post to /poem/update with the id populated.
func TestSavingAgainAfterCreateUpdatesTheSameWork(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	defer srv.Close()

	content := "A bell in snow"
	status, _, headers := postForm(t, srv.URL+"/poem", url.Values{"content": {content}}, map[string]string{"HX-Request": "true"})
	if status != 200 {
		t.Fatalf("POST /poem status = %d, want 200", status)
	}

	redirect := headers.Get("HX-Redirect")
	if !strings.HasPrefix(redirect, "/editor/") {
		t.Fatalf("POST /poem HX-Redirect = %q, want /editor/{id}", redirect)
	}
	id := strings.TrimPrefix(redirect, "/editor/")

	status, body, _ := get(t, srv.URL+redirect, nil)
	if status != 200 {
		t.Fatalf("GET %s status = %d, want 200", redirect, status)
	}
	if !strings.Contains(body, `name="id" value="`+id+`"`) {
		t.Fatalf("the create handoff did not render the work's edit form: %q", body)
	}

	updated := "A bell in thaw"
	status, body, _ = postForm(t, srv.URL+"/poem/update", url.Values{
		"id":      {id},
		"content": {updated},
	}, nil)
	if status != 200 {
		t.Fatalf("POST /poem/update status = %d, want 200", status)
	}
	if !strings.Contains(body, "Updated") {
		t.Fatalf("POST /poem/update missing success text: %q", body)
	}

	var count int
	if err := database.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM poems WHERE deleted_at IS NULL`).Scan(&count); err != nil {
		t.Fatalf("count live poems failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("live poem count = %d, want 1; the second save created a duplicate", count)
	}
}
