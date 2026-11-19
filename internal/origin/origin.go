// Package origin derives the public origin of a request to the authoring service.
//
// A private link is an authoring-service route (/s/{token}), so the URL handed to the author must
// use the origin they used to reach that service -- not PUBLIC_BASE_URL, which is the reader's
// static site and has no such route. Deriving it from the request means a local checkout works with
// no configuration, and a deployed service works behind its own proxy.
package origin

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// ErrInvalid reports a Host or scheme that cannot form a safe absolute URL.
var ErrInvalid = errors.New("invalid request origin")

// RequestScheme reports the scheme a client used to reach the service.
//
// TLS wins. Behind a proxy Render terminates TLS, so r.TLS is nil and the original scheme arrives in
// X-Forwarded-Proto. That header is client-controlled when no trusted proxy overwrites it, so a
// single hop claiming http must not be able to downgrade a link: if any hop in the list reports
// https, the request is treated as https. Otherwise the leftmost non-empty value is used, and a
// request with no header is http.
//
// Callers that gate security decisions must validate the result as http or https. A bare value is
// returned so log formatting can still report an unexpected one.
func RequestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		return "http"
	}
	parts := strings.Split(proto, ",")
	for _, part := range parts {
		if strings.EqualFold(strings.TrimSpace(part), "https") {
			return "https"
		}
	}
	for _, part := range parts {
		if value := strings.ToLower(strings.TrimSpace(part)); value != "" {
			return value
		}
	}
	return "http"
}

// FromRequest returns scheme://host for r, or ErrInvalid if either part is unusable.
//
// Host is validated rather than interpolated blindly. It is normally set by the browser from the
// URL the author visited, but it is still a request header, and a malformed value must not produce
// a capability URL that points somewhere unexpected -- including a userinfo form that could hide
// the real destination from someone copying the link.
//
// Internationalised and percent-encoded hostnames are deliberately rejected: the authoring service
// has an ASCII hostname in every supported deployment, and passing such a value through would
// produce a URL that browsers may resolve differently from the host the request actually used.
func FromRequest(r *http.Request) (string, error) {
	scheme := RequestScheme(r)
	if scheme != "http" && scheme != "https" {
		return "", ErrInvalid
	}

	host := r.Host
	if !validHost(host) {
		return "", ErrInvalid
	}

	return (&url.URL{Scheme: scheme, Host: host}).String(), nil
}

// validHost accepts an authority with no userinfo, path, query, fragment or control characters.
func validHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, r := range host {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(`/@\?#`, r) {
			return false
		}
		// Non-ASCII hosts are not supported; see FromRequest.
		if r > 0x7e {
			return false
		}
	}
	// An IPv6 literal must be bracketed. url.Parse accepts "::1" as a host, but the resulting URL
	// "http://::1" is not a valid authority and would hand the author a broken capability link.
	if strings.Count(host, ":") > 1 && !strings.HasPrefix(host, "[") {
		return false
	}
	parsed, err := url.Parse("//" + host)
	return err == nil && parsed.Host == host && parsed.User == nil && parsed.Path == "" &&
		parsed.RawQuery == "" && parsed.Fragment == ""
}
