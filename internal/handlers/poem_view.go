package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// PoemViewHandler shows a single poem by id.
func PoemViewHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	p, err := services.GetPoem(r.Context(), id)
	if err != nil {
		http.Error(w, "poem not found", http.StatusNotFound)
		return
	}

	renderSurface(w, r, "library", templ.PoemViewScreen(toPoemView(p)))
}

// EditorEditHandler loads a poem into the editor for editing (GET /editor/{id}).
func EditorEditHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	p, err := services.GetPoem(r.Context(), id)
	if err != nil {
		http.Error(w, "poem not found", http.StatusNotFound)
		return
	}

	if editorFullscreen(r) {
		renderSurface(w, r, "editor", templ.EditorWithPoemFullscreen(p.ID, p.Content))
		return
	}

	renderSurface(w, r, "editor", templ.EditorWithPoem(p.ID, p.Content))
}
