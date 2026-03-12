package handlers

import (
	"bytes"
	"net/http"

	"github.com/divijg19/Verse/templ"
)

// EditorHandler renders the editor surface and supports HTMX partial responses.
func EditorHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var buf bytes.Buffer
	if err := templ.Editor().Render(ctx, &buf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// HTMX partial response
	if r.Header.Get("HX-Request") == "true" || r.Header.Get("Hx-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(buf.Bytes())
		return
	}

	// Full page with dynamic nav
	var page bytes.Buffer
	if err := templ.LayoutWithSurface("editor", templ.Editor()).Render(ctx, &page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page.Bytes())
}
