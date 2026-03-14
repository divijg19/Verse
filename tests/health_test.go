package tests

import (
	"strings"
	"testing"
)

func TestHealthEndpoint(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/health", nil)
	if status != 200 {
		t.Fatalf("GET /health status = %d, want 200", status)
	}
	if strings.TrimSpace(body) != "ok" {
		t.Fatalf("GET /health body = %q, want ok", body)
	}
}
