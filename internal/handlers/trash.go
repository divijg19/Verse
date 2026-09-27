package handlers

import (
	"errors"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/divijg19/Verse/internal/presenters"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// PoemTrashHandler lists soft-deleted works.
//
// Reachable for a poem that is itself deleted, which is the point: the recovery path has to be
// reachable at the moment it is wanted, and a deleted work is hidden from the library by definition.
func PoemTrashHandler(w http.ResponseWriter, r *http.Request) {
	trash, err := buildPoemTrash(r, "", "")
	if err != nil {
		writeTrashError(w, r, err)
		return
	}
	renderSurface(w, r, "library", templ.PoemTrashScreen(r.Context(), trash))
}

// RestoreDeletedPoemHandler returns a soft-deleted work to the library.
func RestoreDeletedPoemHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := formWorkID(r)
	if !ok {
		http.Error(w, "missing or malformed id", http.StatusBadRequest)
		return
	}

	if err := services.RestorePoem(r.Context(), id); err != nil {
		if errors.Is(err, services.ErrNotFound) {
			// A repeated restore, or an id for something not deleted. A clean miss rather than a
			// false success, matching SoftDeletePoem's treatment of an already-deleted row.
			http.Error(w, "poem not found in the recycle", http.StatusNotFound)
			return
		}
		log.Printf("restore poem %s: %v", id, err) // #nosec G706 -- validated UUID
		http.Error(w, "failed to restore poem", http.StatusInternalServerError)
		return
	}

	// Re-render the recycle rather than redirecting to the library: seeing the entry disappear is
	// the confirmation, and the restored work's history is one click away if the wrong thing came
	// back.
	trash, err := buildPoemTrash(r, "Restored to your library.", id)
	if err != nil {
		writeTrashError(w, r, err)
		return
	}
	renderSurface(w, r, "library", templ.PoemTrashScreen(r.Context(), trash))
}

func buildPoemTrash(r *http.Request, restored, restoredID string) (templ.PoemTrash, error) {
	poems, err := services.ListDeletedPoems(r.Context(), 100, 0)
	if err != nil {
		return templ.PoemTrash{}, err
	}

	entries := make([]templ.TrashEntry, 0, len(poems))
	for _, p := range poems {
		title := presenters.FirstNonEmptyLine(p.Content)
		if title == "" {
			title = "Untitled"
		}
		entries = append(entries, templ.TrashEntry{
			ID:      p.ID,
			Title:   presenters.TruncateRunes(title, 80),
			Snippet: presenters.TruncateRunes(presenters.FlattenContent(p.Content), 120),
		})
	}

	return templ.PoemTrash{Poems: entries, Restored: restored, RestoredI: restoredID}, nil
}

func writeTrashError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("list recycle: %v", err)
	http.Error(w, "failed to load the recycle", http.StatusInternalServerError)
}
