package handlers

import (
	"bytes"
	"fmt"
	"log"
	"net/http"

	page "github.com/a-h/templ"
	"github.com/divijg19/Verse/internal/htmx"
	views "github.com/divijg19/Verse/templ"
	"github.com/go-chi/chi/v5/middleware"
)

// isHXRequest reports whether r came from htmx. The one definition is in internal/htmx, because
// internal/server asks the same question and the two answers must agree -- see that package's comment.
func isHXRequest(r *http.Request) bool {
	return htmx.IsRequest(r)
}

// fail500 is the only 500 path in this package. The cause goes to the log; the response body stays
// generic, because internal error text is information disclosure even to an authenticated author.
func fail500(w http.ResponseWriter, r *http.Request, message string, err error) {
	// The request id correlates this line with the platform log. #nosec G706: inboundRequestIDMiddleware
	// bounds it and strips control characters before middleware.RequestID stores it, so it cannot forge
	// a log line.
	if id := middleware.GetReqID(r.Context()); id != "" {
		log.Printf("%s (request %s): %v", message, id, err) // #nosec G706
	} else {
		log.Printf("%s: %v", message, err)
	}
	http.Error(w, message, http.StatusInternalServerError)
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
			fail500(w, r, "failed to render view", fmt.Errorf("render %s fragment: %w", surface, err))
			return
		}
		if err := views.NavOOB(surface).Render(ctx, &buf); err != nil {
			fail500(w, r, "failed to render view", fmt.Errorf("render %s nav: %w", surface, err))
			return
		}
		writeHTML(w, buf.Bytes())
		return
	}

	if err := views.Layout(surface, content).Render(ctx, &buf); err != nil {
		fail500(w, r, "failed to render view", fmt.Errorf("render %s page: %w", surface, err))
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
