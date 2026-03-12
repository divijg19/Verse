package handlers

import (
	"bytes"
	"net/http"

	"github.com/divijg19/Verse/templ"
)

// ShareHandler renders the Share surface and supports HTMX partial responses.
func ShareHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var buf bytes.Buffer
	if err := templ.Share().Render(ctx, &buf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" || r.Header.Get("Hx-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(buf.Bytes())
		return
	}

	var page bytes.Buffer
	if err := templ.LayoutWithSurface("share", templ.Share()).Render(ctx, &page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page.Bytes())
}
