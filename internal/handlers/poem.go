package handlers

import (
	"math/rand"
	"net/http"
)

func savePoemHandler(w http.ResponseWriter, r *http.Request) {

	content := r.FormValue("content")

	// Save to database

	w.Write([]byte("Bloom recorded."))
}

func promptHandler(w http.ResponseWriter, r *http.Request) {
	p := prompts[rand.Intn(len(prompts))]
	w.Write([]byte(p))
}
