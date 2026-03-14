package tests

import (
	"net/url"
	"testing"
)

func TestPoemDeleteEndpointSoftDeletes(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A final ember")

	srv := newTestServer(t)
	defer srv.Close()

	status, _, headers := postForm(t, srv.URL+"/poem/delete", url.Values{"id": {id}}, map[string]string{"HX-Request": "true"})
	if status != 200 {
		t.Fatalf("POST /poem/delete status = %d, want 200", status)
	}
	if got := headers.Get("HX-Redirect"); got != "/library" {
		t.Fatalf("HX-Redirect = %q, want /library", got)
	}

	if !poemDeletedByID(t, id) {
		t.Fatalf("poem %s was not soft deleted", id)
	}
}
