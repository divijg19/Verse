package tests

import (
	"net/url"
	"strings"
	"testing"
)

func TestPoemUpdateEndpoint(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "Before the thaw")
	updated := "After the thaw"

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := postForm(t, srv.URL+"/poem/update", url.Values{
		"id":      {id},
		"content": {updated},
	}, nil)
	if status != 200 {
		t.Fatalf("POST /poem/update status = %d, want 200", status)
	}
	if !strings.Contains(body, "Bloom updated") {
		t.Fatalf("POST /poem/update missing success text: %q", body)
	}

	if got := poemContentByID(t, id); got != updated {
		t.Fatalf("updated poem content = %q, want %q", got, updated)
	}
}
