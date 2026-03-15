package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/divijg19/Verse/internal/database"
)

func main() {
	// Initialize database (fail fast if not available)
	if err := database.Connect(); err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	if err := database.EnsureSchema(context.Background()); err != nil {
		log.Fatalf("database schema initialization failed: %v", err)
	}
	defer func() {
		if database.Pool != nil {
			database.Pool.Close()
		}
	}()

	r := newRouter()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Println("listening on :" + port)
	http.ListenAndServe(":"+port, r)
}
