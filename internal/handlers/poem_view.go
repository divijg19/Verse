package handlers

import (
	"fmt"
	"net/http"

	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// PoemViewHandler shows a single poem by id.
func PoemViewHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requirePathWorkID(w, r)
	if !ok {
		return
	}

	p, err := services.GetPoem(r.Context(), id)
	if err != nil {
		writePoemFetchError(w, r, id, err)
		return
	}

	view := toPoemView(p)

	// The publication state is a separate read, because the live reads filter on published_at
	// being null and so cannot report it. A surface that shows the state has to ask for it.
	state, err := services.PublicationState(r.Context(), id)
	if err != nil {
		writePoemFetchError(w, r, id, err)
		return
	}
	view.Published = state.Published
	view.PublishedAt = state.PublishedAt

	renderSurface(w, r, "library", templ.PoemViewScreen(r.Context(), view))
}

// EditorEditHandler loads a poem into the editor for editing (GET /editor/{id}).
func EditorEditHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requirePathWorkID(w, r)
	if !ok {
		return
	}

	p, err := services.GetPoem(r.Context(), id)
	if err != nil {
		writePoemFetchError(w, r, id, err)
		return
	}

	renderSurface(w, r, "editor", templ.EditorWithPoem(r.Context(), p.ID, p.Content))
}

// writePoemFetchError distinguishes "no such work" from "the database could not be asked".
//
// Both handlers previously reported every error as 404, which is wrong in a way that costs real time:
// a Neon outage presented as an empty library, or as a poem that had mysteriously stopped existing,
// with nothing in the log to say the database was unreachable. A 404 asserts a fact about the work,
// and during an outage the service does not know that.
//
// The distinction is a miss or a fault, not a size or a shape. A deleted work is a miss, because
// GetPoem filters it, and telling the author their own work is gone is the same answer as for an id
// that never existed. Everything else -- a dropped connection, a pool timeout, a permissions problem
// -- is a fault, and it is logged with the id so the incident is diagnosable.
//
// The error text is not returned either way. What a caller learns is the status code, and the status
// code is the claim the service can actually support.
func writePoemFetchError(w http.ResponseWriter, r *http.Request, id string, err error) {
	writeMissingWork(w, r, id, "failed to load poem", fmt.Errorf("fetch poem: %w", err))
}
