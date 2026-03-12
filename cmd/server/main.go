package main

import (
	"log"
	"net/http"
	"os"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/handlers"
	"github.com/go-chi/chi/v5"
)

func main() {
	// Initialize database (fail fast if not available)
	if err := database.Connect(); err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer func() {
		if database.Pool != nil {
			database.Pool.Close()
		}
	}()

	r := chi.NewRouter()

	// Serve static files from ./static
	fs := http.FileServer(http.Dir("static"))
	r.Handle("/static/*", http.StripPrefix("/static/", fs))

	// Dashboard as landing page
	r.Get("/", handlers.DashboardHandler)
	r.Get("/dashboard", handlers.DashboardHandler)

	// Editor route (supports HTMX partials)
	r.Get("/editor", handlers.EditorHandler)

	// Caelum screen route (supports HTMX partials)
	r.Get("/caelum", handlers.CaelumHandler)

	// Library and Share placeholders
	r.Get("/library", handlers.LibraryHandler)
	r.Get("/share", handlers.ShareHandler)

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
