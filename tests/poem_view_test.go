package tests

import (
	"strings"
	"testing"
)

func TestPoemViewRouteRendersPoem(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	content := "The river forgets\nNight keeps the names"
	id := insertPoem(t, content)

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/poem/"+id, nil)
	if status != 200 {
		t.Fatalf("GET /poem/{id} status = %d, want 200", status)
	}
	if !strings.Contains(body, "Night keeps the names") {
		t.Fatalf("poem view missing poem content: %q", body)
	}
	if !strings.Contains(body, `hx-get="/library"`) {
		t.Fatalf("poem view missing back link to /library")
	}
	if !strings.Contains(body, `hx-get="/editor/`+id+`"`) {
		t.Fatalf("poem view missing link to /editor/{id}")
	}
}
