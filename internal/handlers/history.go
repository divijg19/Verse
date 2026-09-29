package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

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
	poemID, ok := pathWorkID(r)
	if !ok {
		http.Error(w, "missing or malformed id", http.StatusBadRequest)
		return
	}

	history, err := buildPoemHistory(r, poemID, "")
	if err != nil {
		writeHistoryError(w, poemID, err)
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
	poemID, ok := formWorkID(r)
	if !ok {
		http.Error(w, "missing or malformed id", http.StatusBadRequest)
		return
	}

	versionID, ok := parseWorkID(r.FormValue("version"))
	if !ok {
		http.Error(w, "missing or malformed version", http.StatusBadRequest)
		return
	}

	if err := services.RestorePoemVersion(r.Context(), poemID, versionID); err != nil {
		writeHistoryError(w, poemID, err)
		return
	}

	// Re-render the history rather than redirecting to the work: the confirmation that the restore
	// happened, and the new entry at the top proving it is undoable, are both worth seeing.
	history, err := buildPoemHistory(r, poemID, "Draft restored.")
	if err != nil {
		writeHistoryError(w, poemID, err)
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
			Title:      presenters.TruncateRunes(presenters.FirstNonEmptyLine(v.Content), 80),
			RecordedAt: v.RecordedAt,
		})
	}

	return templ.PoemHistory{
		PoemID:    poem.ID,
		Title:     presenters.TruncateRunes(presenters.FirstNonEmptyLine(poem.Content), 80),
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
func writeHistoryError(w http.ResponseWriter, poemID string, err error) {
	if errors.Is(err, services.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "poem not found", http.StatusNotFound)
		return
	}
	// #nosec G706 -- the id is a UUID validated by pathWorkID or formWorkID above, and the
	// sanitization is the actual mitigation.
	log.Printf("poem history for %s: %v", poemID, err) // #nosec G706
	http.Error(w, "failed to load history", http.StatusInternalServerError)
}

// pathWorkID validates the {id} route parameter as a UUID.
func pathWorkID(r *http.Request) (string, bool) {
	return parseWorkID(chi.URLParam(r, "id"))
}
