package main

import (
	"bytes"
	"log"
	"net/http"
	"os"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/handlers"
	"github.com/divijg19/Verse/templ"
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
	r.Get("/editor", func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		if err := templ.Editor().Render(r.Context(), &buf); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// HTMX partial response
		if r.Header.Get("HX-Request") == "true" || r.Header.Get("Hx-Request") == "true" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(buf.Bytes())
			return
		}

		// Full page response (include fixed nav outside #screen)
		page := "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>Verse</title><link rel=\"stylesheet\" href=\"/static/css/output.css\"><script src=\"https://unpkg.com/htmx.org\"></script><style>#nav-top{position:fixed;top:24px;left:50%;transform:translateX(-50%);}#nav-left{position:fixed;left:24px;top:50%;transform:translateY(-50%);}#nav-right{position:fixed;right:24px;top:50%;transform:translateY(-50%);}#nav-bottom{position:fixed;bottom:24px;left:50%;transform:translateX(-50%);}</style></head><body class=\"bg-neutral-950 text-neutral-200 min-h-screen\"><div id=\"viewport\" class=\"min-h-screen flex items-center justify-center\">" +
			"<div id=\"nav-top\"><button hx-get=\"/caelum\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Caelum\" title=\"Caelum inspiration\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">▲ <span class=\"text-xs block\">Caelum</span></button></div>" +
			"<div id=\"nav-left\"><button hx-get=\"/dashboard\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Dashboard\" title=\"Dashboard\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">◀ <span class=\"text-xs block\">Dashboard</span></button></div>" +
			"<div id=\"nav-right\"><button hx-get=\"/library\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Library\" title=\"Library\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">▶ <span class=\"text-xs block\">Library</span></button></div>" +
			"<div id=\"nav-bottom\"><button hx-get=\"/share\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Share\" title=\"Share\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">▼ <span class=\"text-xs block\">Share</span></button></div>" +
			"<div id=\"screen\" class=\"max-w-3xl w-full transition-all duration-200 ease-out p-8\">" + buf.String() + "</div></div><script src=\"/static/js/navigation.js\"></script></body></html>"

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(page))
	})

	// Caelum screen route (supports HTMX partials)
	r.Get("/caelum", func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		if err := templ.Caelum().Render(r.Context(), &buf); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if r.Header.Get("HX-Request") == "true" || r.Header.Get("Hx-Request") == "true" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(buf.Bytes())
			return
		}

		page := "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>Caelum — Verse</title><link rel=\"stylesheet\" href=\"/static/css/output.css\"><script src=\"https://unpkg.com/htmx.org\"></script><style>#nav-top{position:fixed;top:24px;left:50%;transform:translateX(-50%);}#nav-left{position:fixed;left:24px;top:50%;transform:translateY(-50%);}#nav-right{position:fixed;right:24px;top:50%;transform:translateY(-50%);}#nav-bottom{position:fixed;bottom:24px;left:50%;transform:translateX(-50%);}</style></head><body class=\"bg-neutral-950 text-neutral-200 min-h-screen\"><div id=\"viewport\" class=\"min-h-screen flex items-center justify-center\">" +
			"<div id=\"nav-top\"><button hx-get=\"/caelum\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Caelum\" title=\"Caelum inspiration\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">▲ <span class=\"text-xs block\">Caelum</span></button></div>" +
			"<div id=\"nav-left\"><button hx-get=\"/dashboard\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Dashboard\" title=\"Dashboard\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">◀ <span class=\"text-xs block\">Dashboard</span></button></div>" +
			"<div id=\"nav-right\"><button hx-get=\"/library\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Library\" title=\"Library\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">▶ <span class=\"text-xs block\">Library</span></button></div>" +
			"<div id=\"nav-bottom\"><button hx-get=\"/share\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Share\" title=\"Share\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">▼ <span class=\"text-xs block\">Share</span></button></div>" +
			"<div id=\"screen\" class=\"max-w-3xl w-full transition-all duration-200 ease-out p-8\">" + buf.String() + "</div></div><script src=\"/static/js/navigation.js\"></script></body></html>"

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
