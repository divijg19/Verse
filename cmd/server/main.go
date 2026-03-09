package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func main() {
	r := chi.NewRouter()

	r.Get("/", homeHandler)
	r.Post("/poem", savePoemHandler)

	http.ListenAndServe(":8080", r)
}
