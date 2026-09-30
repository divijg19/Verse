package handlers

import (
	"fmt"
	"net/http"

	"github.com/divijg19/Verse/internal/presenters"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// PoemTrashHandler lists soft-deleted works.
//
// Reachable for a poem that is itself deleted, which is the point: the recovery path has to be
// reachable at the moment it is wanted, and a deleted work is hidden from the library by definition.
func PoemTrashHandler(w http.ResponseWriter, r *http.Request) {
	trash, err := buildPoemTrash(r, "")
	if err != nil {
		writeTrashError(w, r, err)
		return
	}
	renderSurface(w, r, "library", templ.PoemTrashScreen(r.Context(), trash))
}

// RestoreDeletedPoemHandler returns a soft-deleted work to the library.
func RestoreDeletedPoemHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requireFormWorkID(w, r)
	if !ok {
		return
	}

	// A repeated restore, or an id for something not deleted. A clean miss rather than a false
	// success, matching SoftDeletePoem's treatment of an already-deleted row.
	if err := services.RestorePoem(r.Context(), id); err != nil {
		writeMissingWork(w, r, id, "failed to restore poem", fmt.Errorf("restore poem: %w", err))
		return
	}

	// Re-render the recycle rather than redirecting to the library: seeing the entry disappear is
	// the confirmation, and the restored work's history is one click away if the wrong thing came
	// back.
	trash, err := buildPoemTrash(r, "Restored to your library.")
	if err != nil {
		writeTrashError(w, r, err)
		return
	}
	renderSurface(w, r, "library", templ.PoemTrashScreen(r.Context(), trash))
}

func buildPoemTrash(r *http.Request, restored string) (templ.PoemTrash, error) {
	poems, err := services.ListDeletedPoems(r.Context(), 100, 0)
	if err != nil {
		return templ.PoemTrash{}, err
	}

	entries := make([]templ.TrashEntry, 0, len(poems))
	for _, p := range poems {
		entries = append(entries, templ.TrashEntry{
			ID:      p.ID,
			Title:   presenters.WorkTitle(p.Content, presenters.TitleWidthWide),
			Snippet: presenters.TruncateRunes(presenters.FlattenContent(p.Content), 120),
		})
	}

	// The total, so the screen can disclose that the listing is capped rather than truncating
	// silently. Counted separately from the listing because ListDeletedPoems is paged.
	total, err := services.CountDeletedPoems(r.Context())
	if err != nil {
		return templ.PoemTrash{}, err
	}

	return templ.PoemTrash{
		Poems:    entries,
		Restored: restored,
		Total:    total,
	}, nil
}

func writeTrashError(w http.ResponseWriter, r *http.Request, err error) {
	// No ErrNoRows branch, and deliberately: the previous version had one, checking a condition that
	// cannot occur. ListDeletedPoems returns a slice, so its error is a database failure or nothing at
	// all -- never "no such row". The branch was a misconception about what this function returns, and
	// a reader would reasonably have trusted it.
	_ = r
	fail500(w, r, "failed to load the recycle", fmt.Errorf("list recycle: %w", err))
}
