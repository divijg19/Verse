package handlers

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/services"
)

// SavePoemHandler handles saving a poem to the database.
func SavePoemHandler(w http.ResponseWriter, r *http.Request) {
	content := r.FormValue("content")

	if database.Pool == nil {
		http.Error(w, "database not initialized", http.StatusInternalServerError)
		return
	}

	id := uuid.New()
	ctx := r.Context()

	// Insert poem into database
	_, err := database.Pool.Exec(ctx, "INSERT INTO poems (id, content) VALUES ($1, $2)", id.String(), content)
	if err != nil {
		http.Error(w, "failed to save poem", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<span class="text-purple-400 italic">Bloom recorded.</span>`))
}

// UpdatePoemHandler updates an existing poem's content.
func UpdatePoemHandler(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	content := r.FormValue("content")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	if err := services.UpdatePoem(r.Context(), id, content); err != nil {
		http.Error(w, "failed to update poem", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<span class="text-purple-400 italic">Bloom updated.</span>`))
}

// DeletePoemHandler performs a soft-delete of a poem.
func DeletePoemHandler(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	if err := services.SoftDeletePoem(r.Context(), id); err != nil {
		http.Error(w, "failed to delete poem", http.StatusInternalServerError)
		return
	}

	if isHXRequest(r) {
		w.Header().Set("HX-Redirect", "/library")
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Redirect(w, r, "/library", http.StatusSeeOther)
}
