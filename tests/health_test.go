package tests

import (
	"io"
	"net/http"
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

func TestHealthEndpointHead(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodHead, srv.URL+"/health", nil)
	if err != nil {
		t.Fatalf("create HEAD /health request failed: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("execute HEAD /health request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read HEAD /health response failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD /health status = %d, want 200", resp.StatusCode)
	}
	if len(body) != 0 {
		t.Fatalf("HEAD /health body length = %d, want 0", len(body))
	}
}
