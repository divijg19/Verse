package tests

import (
	"strings"
	"testing"
)

func TestSearchEndpointFiltersDeletedPoems(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	insertPoem(t, "Lantern across dark water")
	deletedID := insertPoem(t, "Lantern under ash")
	markPoemDeleted(t, deletedID)

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/poems?q=Lantern", map[string]string{"HX-Request": "true"})
	if status != 200 {
		t.Fatalf("GET /poems status = %d, want 200", status)
	}
	if !strings.Contains(body, "Lantern across dark water") {
		t.Fatalf("search results missing active poem: %q", body)
	}
	if strings.Contains(body, "Lantern under ash") {
		t.Fatalf("search results include soft-deleted poem: %q", body)
	}
}
