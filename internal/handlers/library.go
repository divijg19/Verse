package handlers

import (
	"net/http"

	"github.com/divijg19/Verse/templ"
)

// LibraryHandler renders the Library surface and supports HTMX partial responses.
func LibraryHandler(w http.ResponseWriter, r *http.Request) {
	renderSurface(w, r, "library", templ.Library())
}
