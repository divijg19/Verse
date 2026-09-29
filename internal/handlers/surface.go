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

// fail500 logs a failure and answers 500 with the given message.
//
// Every 500 in this package goes through here, and that is the point. The rule this enforces was
// written down after the poem-view handlers were fixed -- see PoemViewHandler, "a Neon outage
// presented as an empty library, with nothing in the log to say the database was unreachable" --
// and it was then honored by the two handlers that had been fixed and by none of the others.
// Eight paths returned a 500 and said nothing: the library list, the library search fragment, the
// heatmap fragment, the dashboard, and both export failures. A database outage while the author was
// looking at the two surfaces they look at most produced a stream of silent 500s.
//
// A single choke point is the enforcement. The alternative is a convention, and this release found
// eight ways to not follow one.
//
// The message is the response body, so it must stay generic. The cause goes to the log and nowhere
// else, which is the same split the render path has always used: internal error text reaching a
// client is information disclosure, and the client is an authenticated author who already knows
// something is wrong.
func fail500(w http.ResponseWriter, r *http.Request, message string, err error) {
	// The request id is what correlates this line with the platform's own request log, which is the
	// only other place the request appears. Absent from a unit test or a direct handler call, hence
	// the fallback.
	//
	// #nosec G706 -- the id is not raw request input here. inboundRequestIDMiddleware bounds it to
	// maxRequestIDValue and strips control characters before middleware.RequestID stores it, so it
	// cannot forge a log line. The sanitization is the actual mitigation.
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
