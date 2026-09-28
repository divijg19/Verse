package tests

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/migrate"
)

// This file exists because a dashboard load took four of the pool's five connections, and nothing
// measured it.
//
// The dashboard ran four queries in an errgroup, so four connections were held for the length of the
// render. MaxConns defaults to 5. One dashboard load plus one health probe was therefore the entire
// pool, and the probe is the caller that must not be made to wait: the platform uses it to decide
// whether the instance is alive, so a probe queued behind a page view under a slow database fails,
// and failing restarts the instance into the same slow database.
//
// There is no AcquireTimeout here, so a queued request waits rather than failing fast, and
// middleware.Timeout bounds the request rather than the query -- canceling a request does not release
// a connection already blocked in Acquire. The queue is the failure mode, and its size is worth
// asserting rather than reasoning about.
//
// The assertion is on the number of acquisitions, not on the peak held at once. Acquisitions is exact
// and independent of how the two goroutines interleave, so it cannot pass for the wrong reason
// because a query happened to finish before the next one started. The peak is measured too, and
// reported, but the test does not depend on it.

func TestDashboardLoadLeavesTheHealthProbeAConnection(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	// Enough rows that the queries have work to do, and spread over several days so the streak and the
	// heatmap both have something to report.
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		insertPoemAt(t, "a work for the dashboard", now.AddDate(0, 0, -i*3))
	}

	// The server is built from the package's own pool and its own login, so the request below is the
	// production route with a real session. Built before the counting pool is installed, because
	// constructing it is not part of what is being measured.
	srv := newTestServer(t)

	// The pool size is read from the application pool rather than from pgx's own default, which is
	// derived from the CPU count and would be 8 here. The argument this test makes is about the
	// configured 5, so measuring against anything else would make the numbers in the failure message
	// untrue.
	appMaxConns := database.Pool.Config().MaxConns
	counted := newCountingPool(t, appMaxConns)

	// Swapped in for the duration. Every service reads the package-level pool at call time, so the
	// handlers under measurement use this one.
	previous := database.Pool
	database.Pool = counted.pool
	defer func() { database.Pool = previous }()

	counted.acquisitions.Store(0)
	counted.peak.Store(0)

	status, _, _ := get(t, srv.URL+"/dashboard", nil)
	if status != 200 {
		t.Fatalf("GET /dashboard = %d, want 200", status)
	}

	acquired := counted.acquisitions.Load()
	peak := counted.peak.Load()

	// Two is the whole point of the collapse: the days the streak and the heatmap share, and the count
	// and the latest work together. Four left the health probe one connection and no margin at all.
	if acquired != 2 {
		t.Errorf("a dashboard load acquired %d pool connections, want 2.\n"+
			"  MaxConns is 5, so four left the health probe exactly one connection and queued every "+
			"other request behind it. The dashboard should issue one query for the shared days and one "+
			"for the summary.", acquired)
	}
	t.Logf("dashboard: %d acquisitions, %d held at once, of a pool of %d (the application's configured default)",
		acquired, peak, counted.maxConns)

	if peak > 2 {
		t.Errorf("a dashboard load held %d connections at once, want at most 2", peak)
	}
}

// countingPool is a pool that records how often it is acquired and how many connections are held
// simultaneously.
type countingPool struct {
	pool         *pgxpool.Pool
	acquisitions atomic.Int32
	held         atomic.Int32
	peak         atomic.Int32
	maxConns     int32
}

// newCountingPool builds a pool over the same database as the given connection, with the counters
// installed.
//
// The schema is shared with the pool it mirrors rather than being its own, so the counting pool sees
// exactly the rows the test inserted and no migration is needed against it. That also means the
// counter must be reset after construction, which the caller does, because connecting and migrating
// would otherwise be counted alongside the request under measurement.
func newCountingPool(t *testing.T, maxConns int32) *countingPool {
	t.Helper()

	// connectTestDB scopes DATABASE_URL to this package's schema, so the counting pool reaches the
	// same tables without repeating that work.
	scoped := os.Getenv("DATABASE_URL")
	if scoped == "" {
		t.Fatal("DATABASE_URL is unset; connectTestDB must run first")
	}

	cfg, err := pgxpool.ParseConfig(scoped)
	if err != nil {
		t.Fatalf("parse the scoped dsn: %v", err)
	}

	cfg.MaxConns = maxConns
	counted := &countingPool{maxConns: maxConns}
	// PrepareConn, not BeforeAcquire: the latter is deprecated in pgx v5, and PrepareConn takes
	// precedence when both are set, so using the old name would have measured nothing. It fires
	// before every acquisition, which is the event being counted.
	cfg.PrepareConn = func(context.Context, *pgx.Conn) (bool, error) {
		counted.acquisitions.Add(1)
		held := counted.held.Add(1)
		for {
			peak := counted.peak.Load()
			if held <= peak || counted.peak.CompareAndSwap(peak, held) {
				break
			}
		}
		return true, nil
	}
	cfg.AfterRelease = func(*pgx.Conn) bool {
		counted.held.Add(-1)
		return true
	}

	// The pool about to be replaced has already opened its minimum; dropping that first keeps its
	// connections from outliving the Close below and being counted during the request.
	//
	// PrepareConn is only consulted at acquisition time, so the pool already in use would keep handing
	// out uncounted connections. That is why the pool is rebuilt rather than patched. The schema lives
	// in the database, so it survives.
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("build the counting pool: %v", err)
	}
	counted.pool = pool
	t.Cleanup(pool.Close)

	// A single cheap query, so the pool is known to work and the connection is warm. Counted, and
	// therefore reset by the caller before measuring.
	if _, err := pool.Exec(context.Background(), `SELECT 1`); err != nil {
		t.Fatalf("the counting pool is not usable: %v", err)
	}
	if _, err := migrate.Run(context.Background(), pool); err != nil {
		t.Fatalf("migrate through the counting pool: %v", err)
	}
	return counted
}
