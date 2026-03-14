package tests

import "testing"

func TestNavigationSurfaceRoutesReturnOK(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	defer srv.Close()

	headers := map[string]string{"HX-Request": "true"}
	paths := []string{"/editor", "/library", "/caelum", "/share"}

	for _, path := range paths {
		status, _, _ := get(t, srv.URL+path, headers)
		if status != 200 {
			t.Fatalf("GET %s status = %d, want 200", path, status)
		}
	}
}
