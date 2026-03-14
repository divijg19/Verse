package main

import (
	appserver "github.com/divijg19/Verse/internal/server"
	"github.com/go-chi/chi/v5"
)

func newRouter() *chi.Mux {
	return appserver.NewRouter()
}
