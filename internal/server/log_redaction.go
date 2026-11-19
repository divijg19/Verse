package server

import (
	"net/http"
	"regexp"
	"strings"
)

// redactRequestURI keeps capability tokens out of the request log.
//
// chi's default logger writes r.RequestURI verbatim. A private link is a bearer capability carried
// in the URL path, so logging /s/<token> would hand the live credential to everyone with access to
// stdout or the log shipper -- a wider audience than the database that stores only its hash.
//
// The middleware runs before the logger and passes it a shallow copy of the request with only
// RequestURI changed. r.URL is left untouched, because routing and handlers use it; only the string
// the logger prints is redacted. Query strings are dropped too, so no token can hide there.
// privateTokenShape matches the raw link token: 64 lower-case hex characters. The entry route is
// handled by its prefix above; this catches a token in any other path or query component, so a
// future route that carries one does not silently reintroduce the leak.
var privateTokenShape = regexp.MustCompile(`[0-9a-f]{64}`)

func redactRequestURI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uri := r.RequestURI
		if strings.HasPrefix(r.URL.Path, "/s/") {
			uri = "/s/[redacted]"
		} else {
			uri = privateTokenShape.ReplaceAllString(uri, "[redacted]")
		}
		if uri == r.RequestURI {
			next.ServeHTTP(w, r)
			return
		}
		copy := *r
		copy.RequestURI = uri
		next.ServeHTTP(w, &copy)
	})
}
