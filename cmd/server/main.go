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
	"github.com/divijg19/Verse/internal/migrate"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("verse: %v", err)
	}
}

// logMigrations reports what the startup migration did.
//
// Logged rather than kept quiet because a migration that silently applied nothing is
// indistinguishable from one that was never attempted, and a migration that silently applied three
// is worth noticing when diagnosing a deploy. These lines are what the CI deploy-order check and a
// human reading Render's log both look for.
func logMigrations(result migrate.Result) {
	for _, name := range result.Applied {
		log.Printf("applied migration %s", name)
	}
	for _, name := range result.Skipped {
		log.Printf("migration %s already applied", name)
	}

	if len(result.Applied) == 0 {
		log.Printf("schema is up to date; %d migration(s) already applied", len(result.Skipped))
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
	// Apply any pending migrations before serving.
	//
	// The application used to create its own schema on boot from DDL hardcoded in Go, while a
	// separate migrations directory held an identical, independent copy. Nothing kept them equal, so
	// editing the obvious one -- a .sql file -- silently did nothing at runtime. The runner is now
	// the only thing that creates schema, and the .sql files are the only definition of it.
	//
	// This runs in-process rather than as a deploy step because the hosting plan has no pre-deploy
	// hook: that feature is available only for paid web services. Running here means the step cannot
	// be forgotten, and it means the schema can never be ahead of the code that expects it, because
	// both come from the same binary.
	//
	// The cost is that this credential holds DDL rights for the life of the process. Removing that
	// requires a deploy step of some kind; see docs/RUNNING.md.
	//
	// Migrations are idempotent, so this is a no-op on every start after the first. It does run on
	// every start, because a free instance is recycled periodically and each new process repeats it.
	// Bounded: the session timeouts inside the runner are enforced by the server and so cannot help
	// when the client is the side that has stopped hearing back. A free instance is recycled often
	// enough that this is the common path, and a boot that hangs here is a deploy that never
	// completes.
	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), migrate.RunBudget)
	defer cancelMigrate()
	result, err := migrate.Run(migrateCtx, database.Pool)
	if err != nil {
		return errors.New("database migration failed: " + err.Error())
	}
	logMigrations(result)

	// A cheap assertion that the schema the application needs is actually there. Migrations
	// returning success without producing the table would mean the runner and the application
	// disagree about what "migrated" means, which is worth catching at boot rather than on the first
	// query.
	if err := database.RequireSchema(context.Background()); err != nil {
		return errors.New("database schema is not ready: " + err.Error())
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
