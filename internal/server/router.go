package server

import (
	"net/http"

	"github.com/divijg19/Verse/internal/handlers"
	"github.com/go-chi/chi/v5"
)

// NewRouter builds the HTTP route map used by the Verse server.
func NewRouter() *chi.Mux {
	r := chi.NewRouter()

	// Health endpoint (fast, no DB, no templates)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Serve static files from ./static with caching headers.
	fs := http.FileServer(http.Dir("static"))
	staticHandler := http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		fs.ServeHTTP(w, r)
	}))
	r.Handle("/static/*", staticHandler)

	// Dashboard as landing page.
	r.Get("/", handlers.DashboardHandler)
	r.Get("/dashboard", handlers.DashboardHandler)

	// Primary surfaces.
	r.Get("/editor", handlers.EditorHandler)
	r.Get("/caelum", handlers.CaelumHandler)
	r.Get("/library", handlers.LibraryHandler)
	r.Get("/share", handlers.ShareHandler)

	// Poem and editor routes.
	r.Get("/poems", handlers.PoemsHandler)
	r.Get("/poem/{id}", handlers.PoemViewHandler)
	r.Get("/editor/{id}", handlers.EditorEditHandler)

	// Poem lifecycle endpoints.
	r.Post("/poem", handlers.SavePoemHandler)
	r.Post("/poem/update", handlers.UpdatePoemHandler)
	r.Post("/poem/delete", handlers.DeletePoemHandler)

	// Optional prompt endpoint.
	r.Get("/prompt", handlers.PromptHandler)

	return r
}
