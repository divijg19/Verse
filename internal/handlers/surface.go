package handlers

import (
	"bytes"
	"log"
	"net/http"

	page "github.com/a-h/templ"
	views "github.com/divijg19/Verse/templ"
)

func isHXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" || r.Header.Get("Hx-Request") == "true"
}

// renderSurface writes a component either as a full page or, for an HTMX request, as a bare
// fragment with an out-of-band navigation swap appended.
//
// The component is fully buffered before anything is written, so a render error cannot emit a
// half-formed page.
//
// Render errors are logged and reported generically. The previous implementation returned
// err.Error() in the response body, which is information disclosure.
func renderSurface(w http.ResponseWriter, r *http.Request, surface string, content page.Component) {
	ctx := r.Context()
	var buf bytes.Buffer

	if isHXRequest(r) {
		if err := content.Render(ctx, &buf); err != nil {
			log.Printf("render %s fragment: %v", surface, err)
			http.Error(w, "failed to render view", http.StatusInternalServerError)
			return
		}
		if err := views.NavOOB(surface).Render(ctx, &buf); err != nil {
			log.Printf("render %s nav: %v", surface, err)
			http.Error(w, "failed to render view", http.StatusInternalServerError)
			return
		}
		writeHTML(w, buf.Bytes())
		return
	}

	if err := views.Layout(surface, content).Render(ctx, &buf); err != nil {
		log.Printf("render %s page: %v", surface, err)
		http.Error(w, "failed to render view", http.StatusInternalServerError)
		return
	}

	writeHTML(w, buf.Bytes())
}

// writeHTML writes a complete HTML response body, logging a write failure rather than discarding
// it. Shared by every handler in this package that emits a hand-written fragment, so none of them
// can silently ignore a short write.
func writeHTML(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(body); err != nil {
		log.Printf("write response: %v", err)
	}
}
