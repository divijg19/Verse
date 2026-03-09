package main

import (
	"bytes"
	"log"
	"net/http"
	"os"

	"github.com/divijg19/Verse/internal/handlers"
	"github.com/divijg19/Verse/templ"
	"github.com/go-chi/chi/v5"
)

func main() {
	r := chi.NewRouter()

	// Serve static files from ./static
	fs := http.FileServer(http.Dir("static"))
	r.Handle("/static/*", http.StripPrefix("/static/", fs))

	// Render editor at / by composing the editor HTML into a simple layout
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		if err := templ.Editor().Render(r.Context(), &buf); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		page := "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>`Verse`</title><link rel=\"stylesheet\" href=\"/static/css/output.css\"><script src=\"https://unpkg.com/htmx.org\"></script></head><body class=\"bg-neutral-950 text-neutral-200 min-h-screen\"><div class=\"max-w-3xl mx-auto p-8\">" + buf.String() + "</div></body></html>"

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(page))
	})

	// HTMX endpoint for saving poems
	r.Post("/poem", handlers.SavePoemHandler)
	// Optional: prompt endpoint
	r.Get("/prompt", handlers.PromptHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Println("listening on :" + port)
	http.ListenAndServe(":"+port, r)
}
