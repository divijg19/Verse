package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/divijg19/Verse/templ"
)

// Helpers shared by the public Share endpoint and the private-link endpoints.
//
// They live together because both surfaces encode a small JSON response the same way. Their origins
// are deliberately different: shareAddress is the reader's static site and builds the public
// address Copy Link copies, while a private link is an authoring-service route built from the origin
// that served the request (see internal/origin). Conflating the two produces a link that 404s.

// writeJSONNoStore writes a small JSON response with the headers every capability-bearing response
// needs. Both the Share and guest endpoints use it so the media type cannot drift.
func writeJSONNoStore(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write the JSON response: %v", err)
	}
}

// shareAddress is the reader-facing base, shared by the share data and the link endpoints.
//
// A malformed value must not become a link copied with a success message, so the value is parsed
// here. An empty string means "not configured" and is the only safe fallback: the authoring service
// cannot know the reader's origin from the request, because the two are different services.
func shareAddress() string {
	value := strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL"))
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	return strings.TrimRight(value, "/")
}

// guestLinkPath is the prefix of every shareable private link.
const guestLinkPath = "/s/"

// publicLinkPath is the prefix of every shareable public submission link.
const publicLinkPath = "/p/"

// linkURL builds a shareable URL on the authoring service's own origin.
//
// The base comes from the request, not PUBLIC_BASE_URL: the latter names the reader's static site,
// which has no /s/{token} or /p/{token} route. There is deliberately no relative form -- a path
// nobody else can open would be worse than a refused request. Both link kinds go through this one
// builder so base trimming and escaping cannot drift apart.
func linkURL(base, token, path string) string {
	return strings.TrimRight(base, "/") + path + url.PathEscape(token)
}

// linkState is the single place where a Share row's state is decided. Precedence is deliberate:
// revocation is explicit and irreversible, expiry is a clock fact, publication is a work-level
// condition, and an unrecoverable token still works for whoever already holds it. A row is never
// called "ended" merely because Copy is unavailable.
func linkState(revoked, expired, published, recoverable bool) templ.LinkState {
	switch {
	case revoked:
		return templ.LinkStateRevoked
	case expired:
		return templ.LinkStateExpired
	case !published:
		return templ.LinkStateUnpublished
	case !recoverable:
		return templ.LinkStateUnrecoverable
	default:
		return templ.LinkStateActive
	}
}
