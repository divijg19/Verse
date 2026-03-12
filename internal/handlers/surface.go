package handlers

import (
	"bytes"
	"net/http"

	page "github.com/a-h/templ"
	views "github.com/divijg19/Verse/templ"
)

func isHXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" || r.Header.Get("Hx-Request") == "true"
}

func renderSurface(w http.ResponseWriter, r *http.Request, surface string, content page.Component) {
	ctx := r.Context()
	var buf bytes.Buffer

	if isHXRequest(r) {
		if err := content.Render(ctx, &buf); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := views.NavOOB(surface).Render(ctx, &buf); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(buf.Bytes())
		return
	}

	if err := views.Layout(surface, content).Render(ctx, &buf); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}
