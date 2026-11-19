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
// They live together because the two surfaces make the same two decisions: how to encode a small
// JSON response without letting it be cached, and where the reader's origin is. Having one spelling
// of each matters more here than it does elsewhere -- a public address built with a different rule
// on the two surfaces would hand out two different links to the same work.

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

// guestLinkURL builds the shareable URL the author copies.
//
// The absolute form is required, not a convenience: ShareLinkHandler refuses to mint a link without
// PUBLIC_BASE_URL, so this is only ever called with a configured origin. There is deliberately no
// relative fallback -- a path nobody else can open would be worse than a refused request.
func guestLinkURL(token string) string {
	return shareAddress() + guestLinkPath + url.PathEscape(token)
}
