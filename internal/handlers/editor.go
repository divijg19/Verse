package handlers

import (
	"net/http"

	"github.com/divijg19/Verse/templ"
)

// EditorHandler renders the editor surface and supports HTMX partial responses.
func EditorHandler(w http.ResponseWriter, r *http.Request) {
	renderSurface(w, r, "editor", templ.Editor())
}
