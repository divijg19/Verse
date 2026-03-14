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
	status, body, _ := postForm(t, srv.URL+"/poem", url.Values{"content": {content}}, nil)
	if status != 200 {
		t.Fatalf("POST /poem status = %d, want 200", status)
	}
	if !strings.Contains(body, "Bloom recorded") {
		t.Fatalf("POST /poem body missing success text: %q", body)
	}

	var count int
	if err := database.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM poems WHERE content = $1 AND deleted_at IS NULL`, content).Scan(&count); err != nil {
		t.Fatalf("count inserted poem failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("inserted poem count = %d, want 1", count)
	}
}
