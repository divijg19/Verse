package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedactRequestURIKeepsPrivateLinkTokensOutOfLogs(t *testing.T) {
	var seen string
	handler := redactRequestURI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RequestURI
		w.WriteHeader(http.StatusOK)
	}))

	for _, tc := range []struct{ path, want string }{
		{"/s/0123456789abcdef", "/s/[redacted]"},
		{"/s/0123456789abcdef?x=1", "/s/[redacted]"},
		{"/library", "/library"},
		{"/library?next=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "/library?next=[redacted]"},
		{"/elsewhere/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "/elsewhere/[redacted]"},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if seen != tc.want {
			t.Errorf("logger would see %q for %q, want %q", seen, tc.path, tc.want)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d", rec.Code)
		}
	}

	// Routing and handlers still see the real URL; only the logged string was changed.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/s/0123456789abcdef", nil)
	rec := httptest.NewRecorder()
	redactRequestURI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/s/0123456789abcdef" {
			t.Errorf("routing URL was changed to %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, "ok")
	})).ServeHTTP(rec, req)
}
