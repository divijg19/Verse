package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Helpers shared by the public Share endpoint and the private-link endpoints.
//
// They live together because both surfaces encode a small JSON response the same way. Their origins
// are deliberately different: shareAddress is the reader's static site and builds the public
// address Copy Link copies, while a private link is an authoring-service route built from the origin
// that served the request (see internal/origin). Conflating the two produces a link that 404s.

// shareJSON writes a small JSON response with the no-store header the share endpoints share.
func shareJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write the share response: %v", err)
	}
}

// shareAddress is the reader-facing base, shared by the share data and the link endpoints.
func shareAddress() string {
	return strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/")
}

// guestLinkURL builds the shareable URL the author copies on the authoring service's own origin.
//
// The base comes from the request, not PUBLIC_BASE_URL: the latter names the reader's static site,
// which has no /s/{token} route. There is deliberately no relative form -- a path nobody else can
// open would be worse than a refused request.
func guestLinkURL(base, token string) string {
	return strings.TrimRight(base, "/") + guestLinkPath + url.PathEscape(token)
}
