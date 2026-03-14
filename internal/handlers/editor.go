package handlers

import (
	"net/http"

	"github.com/divijg19/Verse/templ"
)

// EditorHandler renders the editor surface and supports HTMX partial responses.
func EditorHandler(w http.ResponseWriter, r *http.Request) {
	if editorFullscreen(r) {
		renderSurface(w, r, "editor", templ.EditorFullscreen())
		return
	}

	renderSurface(w, r, "editor", templ.Editor())
}

func editorFullscreen(r *http.Request) bool {
	return r.URL.Query().Get("fullscreen") == "1"
}
