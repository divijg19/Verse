package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/divijg19/Verse/internal/presenters"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// PoemHistoryHandler renders a poem's retained history.
//
// Reachable for a soft-deleted poem as well as an active one. A deleted work still has a row, and
// its history is exactly what someone trying to recover an accidental deletion needs; requiring the
// poem to be active would make the recovery path unreachable at the moment it is wanted.
func PoemHistoryHandler(w http.ResponseWriter, r *http.Request) {
	poemID, ok := requirePathWorkID(w, r)
	if !ok {
		return
	}

	history, err := buildPoemHistory(r, poemID, "")
	if err != nil {
		writeHistoryError(w, r, poemID, err)
		return
	}

	renderSurface(w, r, "library", templ.PoemHistoryScreen(r.Context(), history))
}

// RestorePoemVersionHandler makes a superseded draft the poem's current text.
//
// The restore is performed as an ordinary edit by the service layer, so the draft it displaces is
// retained in turn. That is what makes a restore undoable rather than a one-way door, and it is why
// there is no separate "purge history" path here: nothing in the application edits or deletes a
// retained version.
func RestorePoemVersionHandler(w http.ResponseWriter, r *http.Request) {
	poemID, ok := requireFormWorkID(w, r)
	if !ok {
		return
	}

	versionID, ok := parseWorkID(r.FormValue("version"))
	if !ok {
		http.Error(w, "missing or malformed version", http.StatusBadRequest)
		return
	}

	if err := services.RestorePoemVersion(r.Context(), poemID, versionID); err != nil {
		writeHistoryError(w, r, poemID, err)
		return
	}

	// Re-render the history rather than redirecting to the work: the confirmation that the restore
	// happened, and the new entry at the top proving it is undoable, are both worth seeing.
	history, err := buildPoemHistory(r, poemID, "Draft restored.")
	if err != nil {
		writeHistoryError(w, r, poemID, err)
		return
	}
	renderSurface(w, r, "library", templ.PoemHistoryScreen(r.Context(), history))
}

// buildPoemHistory assembles the history screen for a poem.
//
// A soft-deleted poem is loaded without the deleted_at filter that GetPoem applies, because its
// history has to stay reachable while the work itself is hidden from the library.
func buildPoemHistory(r *http.Request, poemID, restored string) (templ.PoemHistory, error) {
	// GetPoemIncludingDeleted, not GetPoem: a soft-deleted work's history has to stay reachable
	// while the work itself is hidden from the library.
	poem, err := services.GetPoemIncludingDeleted(r.Context(), poemID)
	if err != nil {
		return templ.PoemHistory{}, err
	}

	versions, err := services.ListPoemVersions(r.Context(), poemID)
	if err != nil {
		return templ.PoemHistory{}, err
	}

	views := make([]templ.PoemVersionView, 0, len(versions))
	for _, v := range versions {
		views = append(views, templ.PoemVersionView{
			ID:         v.ID,
			PoemID:     v.PoemID,
			Content:    v.Content,
			Title:      presenters.WorkTitle(v.Content, presenters.TitleWidthWide),
			RecordedAt: v.RecordedAt,
		})
	}

	return templ.PoemHistory{
		PoemID:    poem.ID,
		Title:     presenters.WorkTitle(poem.Content, presenters.TitleWidthWide),
		Current:   poem.Content,
		Versions:  views,
		Restored:  restored,
		DeletedAt: poem.DeletedAt,
	}, nil
}

// writeHistoryError maps a history failure onto a response, distinguishing a clean miss from a fault.
//
// pgx.ErrNoRows reaches here as a miss rather than a 500: a mistyped or stale id is an ordinary thing
// for the author to do, and reporting it as a server error would be both wrong and alarming.
func writeHistoryError(w http.ResponseWriter, r *http.Request, poemID string, err error) {
	writeMissingWork(w, r, poemID, "failed to load history", fmt.Errorf("poem history: %w", err))
}

// writeMissingWork answers a failure about one named work: a miss is a 404, a fault is a logged 500.
//
// Four sites were writing this block, with the same shape and the same two outcomes. The rule is
// worth stating once because the distinction is not cosmetic: a mistyped or stale id is an ordinary
// thing for the author to do, and reporting it as a server error is both wrong and alarming, while a
// genuine fault reported as a 404 reads as "this does not exist" when in fact the service could not
// ask.
//
// One condition, because every read and mutation in services now reports a missing row as
// services.ErrNotFound. Two of these sites used to test for pgx.ErrNoRows as well, which was only
// necessary because the single-work reads returned the driver error unwrapped -- so a caller had to
// know which read it had called to know what to test for.
func writeMissingWork(w http.ResponseWriter, r *http.Request, id, what string, err error) {
	if errors.Is(err, services.ErrNotFound) {
		http.Error(w, "poem not found", http.StatusNotFound)
		return
	}
	// The id is a UUID validated by requirePathWorkID or requireFormWorkID above, so it cannot carry
	// a newline and forge a log line. fail500 additionally logs the request id, which
	// inboundRequestIDMiddleware has already bounded and stripped.
	fail500(w, r, what, fmt.Errorf("%s for %s: %w", what, id, err))
}

// pathWorkID validates the {id} route parameter as a UUID.
func pathWorkID(r *http.Request) (string, bool) {
	return parseWorkID(chi.URLParam(r, "id"))
}
