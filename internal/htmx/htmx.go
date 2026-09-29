// Package htmx holds the one predicate that decides how a request is answered.
//
// It is its own package for a reason that is not tidiness. Two identical copies of this function
// existed -- handlers.isHXRequest and server.wantsHTMLRedirect -- and they answer the same question
// in two places that must agree. One decides whether a component is rendered as a bare fragment;
// the other decides whether an unauthenticated request is answered with a 303 redirect or a bare
// 401. If the two ever disagreed about a header, an HTMX request that was not recognized by one
// would be answered by the other, and the result is a redirect to a page the fragment-only caller
// never asked for.
//
// The packages cannot share code the obvious way: internal/server imports internal/handlers, so
// handlers cannot import server. This leaf sits under both.
//
// The doubled header lookup is deliberate and is not a bug to clean up. HTTP header names are
// case-insensitive and canonicalised by net/http, so a request carrying htmx's "HX-Request" arrives
// as "Hx-Request" in Go's map. The fallback covers a client that set the header on the raw request
// line in a form Go's canonicalisation does not produce.
package htmx

import "net/http"

// IsRequest reports whether r came from htmx rather than from a browser navigation.
//
// The distinction decides two things that must not be decided differently: whether a response is a
// full page or a fragment, and whether a refusal is a redirect or a bare status. htmx sets the
// header on every request it makes, including the ones a form posts.
func IsRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" || r.Header.Get("Hx-Request") == "true"
}
