package handlers

import (
	"fmt"
	"net/http"

	"github.com/divijg19/Verse/internal/export"
)

// ExportHandler streams an export of the whole body of work as a download.
//
// Sits inside the authenticated group. The export is the entire library, so serving it publicly
// would publish everything, which is the opposite of what this service is for.
//
// Registered for GET and HEAD, and safe as a GET: it needs no synchroniser token precisely because
// it changes nothing, and every route that does change something is a POST.
func ExportHandler(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")

	writer, ok := export.ByName(format)
	if !ok {
		http.Error(w, "unknown format; want json or md", http.StatusBadRequest)
		return
	}

	// Soft-deleted works are included by default here, unlike the command's default. The reasoning
	// is the opposite: a download clicked in a browser is an archival act, and an archive that
	// silently omitted the deleted works would be a partial copy of the thing it claims to preserve.
	doc, err := export.Build(r.Context(), true)
	if err != nil {
		http.Error(w, "failed to build the export", http.StatusInternalServerError)
		return
	}

	body, err := writer.Write(doc)
	if err != nil {
		http.Error(w, "failed to write the export", http.StatusInternalServerError)
		return
	}

	// A dated filename, so a browser that keeps several does not silently overwrite one with
	// another. The stamp is the export's own, not the request's, so two downloads of unchanged work
	// are comparable.
	filename := fmt.Sprintf("verse-%s.%s", doc.ExportedAt.UTC().Format("2006-01-02"), writer.FileExtension())
	w.Header().Set("Content-Type", writer.ContentType())
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	// The whole point is to keep a copy, so it must not be cached by anything in between.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	// Poem content is written through unescaped, deliberately. Sanitizing it would corrupt the very
	// thing this route exists to preserve, and the archive is not a document anyone renders: the
	// content type comes from a closed set that is never text/html, X-Content-Type-Options: nosniff
	// is set on every response by securityHeadersMiddleware, and Content-Disposition: attachment
	// forces a download. A <script> in a poem is data here, and treating it as markup is precisely
	// what must not happen.
	//
	// #nosec G705 -- the suppression covers the taint rule only. The measures above are the actual
	// mitigation; gosec does not model the content type or the disposition.
	if _, err := w.Write(body); err != nil { // #nosec G705
		// The download has already begun, so there is no status left to change.
		return
	}
}
