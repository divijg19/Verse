package handlers

import (
	"math/rand"
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
	w.Write([]byte(`<span class="text-purple-400">Bloom recorded.</span>`))
}

// PromptHandler returns a random writing prompt.
func PromptHandler(w http.ResponseWriter, r *http.Request) {
	p := services.Prompts[rand.Intn(len(services.Prompts))]
	w.Write([]byte(p))
}
