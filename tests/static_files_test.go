package tests

import (
	"os"
	"strings"
	"testing"
)

func TestStaticFilesServed(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	paths := []string{"/static/js/navigation.js"}
	if _, err := os.Stat("static/css/output.css"); err == nil {
		paths = append(paths, "/static/css/output.css")
	}
	for _, path := range paths {
		status, _, headers := get(t, srv.URL+path, nil)
		if status != 200 {
			t.Fatalf("GET %s status = %d, want 200", path, status)
		}
		if !strings.Contains(headers.Get("Cache-Control"), "max-age=86400") {
			t.Fatalf("GET %s missing cache header, got %q", path, headers.Get("Cache-Control"))
		}
	}
}
