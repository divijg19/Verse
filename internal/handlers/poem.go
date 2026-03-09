package handlers

import (
	"math/rand"
	"net/http"

	"github.com/divijg19/Verse/internal/services"
)

// SavePoemHandler handles saving a poem (temporarily discards content).
func SavePoemHandler(w http.ResponseWriter, r *http.Request) {
	content := r.FormValue("content")
	_ = content

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<span class="text-purple-400">Bloom recorded.</span>`))
}

// PromptHandler returns a random writing prompt.
func PromptHandler(w http.ResponseWriter, r *http.Request) {
	p := services.Prompts[rand.Intn(len(services.Prompts))]
	w.Write([]byte(p))
}
