package handlers

import (
	"bytes"
	"net/http"

	"github.com/divijg19/Verse/templ"
)

// CaelumHandler renders the Caelum surface and supports HTMX partial responses.
func CaelumHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var buf bytes.Buffer
	if err := templ.Caelum().Render(ctx, &buf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" || r.Header.Get("Hx-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(buf.Bytes())
		return
	}

	var page bytes.Buffer
	if err := templ.LayoutWithSurface("caelum", templ.Caelum()).Render(ctx, &page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page.Bytes())
}
