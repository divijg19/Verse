package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/migrate"
)

// version is the commit this binary was built from, supplied by the build.
//
// Set with -ldflags "-X main.version=...". A `go run` or a plain `go build` leaves it as "dev", which
// is itself informative: it distinguishes a local process from a deployed one in the log.
var version = "dev"

func main() {
	// The build stamps this with the commit. Render's log is the only place it can be read, and
	// "which commit is running" is a question an operator asks during every incident -- so it is
	// answered by the first line of the boot log rather than requiring a round trip to the dashboard.
	//
	// Both build paths stamp it: the Dockerfile takes it as a build argument, and render.yaml derives
	// it with `git rev-parse` because a native build has no equivalent. That second half was missing
	// when this was first written, and this comment claimed it existed -- so the production build
	// logged "dev" on every boot while the comment said otherwise. Declaring the variable is only half
	// the fix; passing it is the other half, and
	// TestRenderYamlBuildCommandStampsTheVersion now reads render.yaml's buildCommand to check the
	// two have not drifted apart again.
	log.Printf("verse starting, version %s", version)
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

// schemaCheckBudget bounds the post-migration schema assertion.
//
// Short, because it runs against a database that has just answered a migration query and a
// connection, and the only thing left to verify is a single to_regclass lookup. A budget long enough
// to accommodate a slow network would be indistinguishable from no budget at all.
const schemaCheckBudget = 10 * time.Second

// applyMigrations runs the startup migration and disposes of the credential it used.
//
// Two arrangements, chosen by whether MIGRATION_DATABASE_URL is set:
//
//	set    a separate, privileged pool is opened, used, and closed. The serving credential in
//	       DATABASE_URL can then be stripped of every DDL right, which is the whole point.
//	unset  the same pool the server will use is opened, migrated, and kept. This is what every
//	       release before this one did, and it is what a single-role database requires.
//
// The fallback is deliberate rather than a convenience: a deployment that has not yet created the
// second role must keep booting, and a service that refuses to start because an optional variable is
// absent would be a worse outcome than a service that still holds DDL rights. The log says which
// arrangement is in force, so the weaker one is never silent.
func applyMigrations() error {
	envVar := "MIGRATION_DATABASE_URL"
	if os.Getenv(envVar) == "" {
		envVar = "DATABASE_URL"
		log.Printf("MIGRATION_DATABASE_URL is not set; migrating with %s, which means the serving "+
			"credential still holds DDL rights. Set it to a separate owner credential to drop those; "+
			"see RUNNING.md", envVar)
	}

	// Which pool gets migrated depends on what the caller has already connected, and today the
	// answer is always "none": run() calls this before database.Connect(), so previous is nil and
	// the branch below that takes ownership is the one taken.
	//
	// The other branch is written anyway, and deliberately. If the boot order is ever changed so
	// that Connect runs first, the correct behavior is to migrate over the caller's pool and leave
	// it open -- and a function that assumed the order would instead close a pool the caller still
	// holds. Two lines now, rather than a subtle bug later. The alternative, asserting the order,
	// would be a stronger promise than this function can actually make about its caller.
	previous := database.Pool
	defer func() { database.Pool = previous }()

	pool, err := database.Open(envVar)
	if err != nil {
		if envVar == "DATABASE_URL" {
			return errors.New("database connection failed: " + err.Error())
		}
		return errors.New("migration database connection failed: " + err.Error())
	}

	// The caller may already hold a pool, in which case that is the one to use rather than opening a
	// second connection to the same database. This is the fallback arrangement.
	if previous != nil {
		pool.Close()
		pool = previous
	} else {
		database.Pool = pool
		defer func() {
			pool.Close()
			database.Pool = nil
		}()
	}

	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), migrate.RunBudget)
	defer cancelMigrate()

	result, err := migrate.Run(migrateCtx, pool)
	if err != nil {
		return errors.New("database migration failed: " + err.Error())
	}
	logMigrations(result)
	return nil
}

// run wires and serves the application, returning an error instead of exiting so that deferred
// cleanup actually executes. The previous implementation called log.Fatalf on the listen path,
// which made its deferred database.Pool.Close() unreachable and dropped in-flight requests on deploy.
func run() error {
	// Apply any pending migrations before serving, over a pool that is thrown away immediately after.
	//
	// The application used to create its own schema on boot from DDL hardcoded in Go, while a
	// separate migrations directory held an identical, independent copy. Nothing kept them equal, so
	// editing the obvious one -- a .sql file -- silently did nothing at runtime. The runner is now
	// the only thing that creates schema, and the .sql files are the only definition of it.
	//
	// This runs in-process rather than as a deploy step because the hosting plan has no pre-deploy
	// hook: that feature is available only for paid compute plans. Running here means the step cannot
	// be forgotten, and it means the schema can never be ahead of the code that expects it, because
	// both come from the same binary.
	//
	// What it does NOT follow is that the serving credential therefore needs DDL. Those are two
	// separate facts, and conflating them is what kept the runtime role as an owner for so long. The
	// migration needs CREATE and ALTER; nothing that serves a request does. So the migration gets its
	// own pool, from MIGRATION_DATABASE_URL, and that pool is closed before the first request is
	// served. The serving pool connects afterwards with DATABASE_URL, which can hold nothing but
	// SELECT, INSERT and UPDATE.
	//
	// MIGRATION_DATABASE_URL is optional. Unset, the migration reuses DATABASE_URL, which is the
	// behavior of every release before this one and the only arrangement that works against a
	// single-role database such as the local one in compose.yaml. Set, it is a separate connection
	// with its own rights. RUNNING.md gives the role setup.
	//
	// Migrations are idempotent, so this is a no-op on every start after the first. It does run on
	// every start, because a free instance is recycled periodically and each new process repeats it.
	// Bounded: the session timeouts inside the runner are enforced by the server and so cannot help
	// when the client is the side that has stopped hearing back. A free instance is recycled often
	// enough that this is the common path, and a boot that hangs here is a deploy that never
	// completes.
	if err := applyMigrations(); err != nil {
		return err
	}

	// Open the serving pool. This is the credential the process holds for its whole life, and the
	// only one it still holds once the migration pool above has been closed.
	if err := database.Connect(); err != nil {
		return errors.New("database connection failed: " + err.Error())
	}

	// A cheap assertion that the schema the application needs is actually there. Migrations
	// returning success without producing the table would mean the runner and the application
	// disagree about what "migrated" means, which is worth catching at boot rather than on the first
	// query.
	//
	// Bounded, unlike its neighbors until now. The migration call is wrapped in RunBudget and this
	// one was passed context.Background(), so a database that accepted the connection and then
	// stopped answering would hang the boot with no Go-side deadline -- and a boot that hangs is a
	// deploy that never completes, which is the same failure the RunBudget comment above describes.
	schemaCtx, cancelSchema := context.WithTimeout(context.Background(), schemaCheckBudget)
	defer cancelSchema()
	if err := database.RequireSchema(schemaCtx); err != nil {
		return errors.New("database schema is not ready: " + err.Error())
	}
	defer database.ClosePool()

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
