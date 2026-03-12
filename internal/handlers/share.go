package handlers

import (
	"net/http"

	"github.com/divijg19/Verse/templ"
)

// ShareHandler renders the Share surface and supports HTMX partial responses.
func ShareHandler(w http.ResponseWriter, r *http.Request) {
	renderSurface(w, r, "share", templ.Share())
}
