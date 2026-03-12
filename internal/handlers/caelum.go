package handlers

import (
	"net/http"

	"github.com/divijg19/Verse/templ"
)

// CaelumHandler renders the Caelum surface and supports HTMX partial responses.
func CaelumHandler(w http.ResponseWriter, r *http.Request) {
	renderSurface(w, r, "caelum", templ.Caelum())
}
