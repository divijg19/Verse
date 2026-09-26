package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/divijg19/Verse/internal/database"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("verse: %v", err)
	}
}

// run wires and serves the application, returning an error instead of exiting so that deferred
// cleanup actually executes. The previous implementation called log.Fatalf on the listen path,
// which made its deferred database.Pool.Close() unreachable and dropped in-flight requests on deploy.
func run() error {
	// Initialize database (fail fast if not available)
	if err := database.Connect(); err != nil {
		return errors.New("database connection failed: " + err.Error())
	}
	if err := database.EnsureSchema(context.Background()); err != nil {
		return errors.New("database schema initialization failed: " + err.Error())
	}
	defer func() {
		if database.Pool != nil {
			database.Pool.Close()
		}
	}()

	// Canceled on SIGINT or SIGTERM, giving in-flight requests a bounded window to finish.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Built before the server so a configuration failure is reported as a clear error, naming the
	// missing variables, rather than as a panic from inside the router.
	handler, err := newRouter()
	if err != nil {
		return err
	}

	cfg := appserverConfig()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		ErrorLog:          log.Default(),
	}

	// Serve in a goroutine so the signal handler owns the main path.
	serveErr := make(chan error, 1)
	go func() {
		log.Printf("listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		log.Println("shutdown signal received, draining connections")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		// Connections outlived the grace period; force them closed so the process can exit.
		log.Printf("graceful shutdown incomplete, closing: %v", err)
		if closeErr := srv.Close(); closeErr != nil {
			log.Printf("forced close: %v", closeErr)
		}
		return err
	}

	log.Println("shutdown complete")
	return nil
}
