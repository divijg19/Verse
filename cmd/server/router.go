package main

import (
	appserver "github.com/divijg19/Verse/internal/server"
	"github.com/go-chi/chi/v5"
)

// newRouter builds the HTTP route map. See appserver.NewRouter for the security boundary and for
// why a missing authentication configuration is returned as an error rather than panicking.
func newRouter() (*chi.Mux, error) {
	return appserver.NewRouter()
}

// appserverConfig exposes the authoring server's validated runtime configuration to main.
func appserverConfig() appserver.ServerConfig {
	return appserver.LoadServerConfig()
}
