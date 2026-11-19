package origin

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFromRequest(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		tls     bool
		proto   string
		want    string
		wantErr bool
	}{
		{name: "plain local", host: "localhost:8080", want: "http://localhost:8080"},
		{name: "tls", host: "verse.onrender.com", tls: true, want: "https://verse.onrender.com"},
		{name: "proxy proto", host: "verse.onrender.com", proto: "https", want: "https://verse.onrender.com"},
		{name: "proto list", host: "verse.onrender.com", proto: "https, http", want: "https://verse.onrender.com"},
		{name: "proto list downgrade attempt", host: "verse.onrender.com", proto: "http, https", want: "https://verse.onrender.com"},
		{name: "empty leftmost proto", host: "verse.onrender.com", proto: ", https", want: "https://verse.onrender.com"},
		{name: "uppercase proto", host: "verse.onrender.com", proto: "HTTPS", want: "https://verse.onrender.com"},
		{name: "bracketed ipv6", host: "[::1]:8080", want: "http://[::1]:8080"},
		{name: "unbracketed ipv6", host: "::1", wantErr: true},
		{name: "idn host", host: "例え.テスト", wantErr: true},
		{name: "bad proto", host: "verse.onrender.com", proto: "javascript", wantErr: true},
		{name: "userinfo host", host: "evil@example.com", wantErr: true},
		{name: "path in host", host: "example.com/s", wantErr: true},
		{name: "query in host", host: "example.com?x=1", wantErr: true},
		{name: "control in host", host: "example.com\nX: y", wantErr: true},
		{name: "empty host", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/share", nil)
			req.Host = tc.host
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.proto)
			}

			got, err := FromRequest(req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("FromRequest = %q, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("FromRequest: %v", err)
			}
			if got != tc.want {
				t.Errorf("FromRequest = %q, want %q", got, tc.want)
			}
		})
	}
}
