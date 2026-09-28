package tests

import (
	"context"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/migrate"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/internal/testsupport"
)

// nonUTC is the session zone these tests run under.
//
// +05:30 specifically, because it is the offset 004_login_rate_limit.sql names as the cause of a real
// Retry-After of 19855 seconds against a cap of 900. Using the offset this codebase has already been
// bitten by, rather than an arbitrary one, means the test and the incident argue for each other.
const nonUTC = "Asia/Kolkata"

// useSessionInZone points the application at a scratch schema whose every connection runs in the named
// zone, and returns the function that puts the previous pool back.
//
// The application reads the package-level pool, so this is the only way to exercise a query under a
// zone other than the one it is configured for. connectTestDB cannot be used: it calls
// database.Connect, which pins sessions to UTC, and pinning is exactly what has to be tested around.
// The suite's tests are sequential -- there is no t.Parallel anywhere in it -- so swapping the pool
// for the duration is safe.
func useSessionInZone(t *testing.T, zone string) {
	t.Helper()

	dsn := requireTestDSN(t)
	schema := testsupport.NormalizeSchemaName("zone_t_", t.Name())

	pool, cleanup, err := testsupport.ConnectScratchInZone(context.Background(), dsn, schema, zone)
	if err != nil {
		t.Fatalf("scratch schema in %s: %v", zone, err)
	}
	if _, err := migrate.Run(context.Background(), pool); err != nil {
		_ = cleanup()
		t.Fatalf("migrate the scratch schema: %v", err)
	}

	var got string
	if err := pool.QueryRow(context.Background(), `SHOW TimeZone`).Scan(&got); err != nil {
		_ = cleanup()
		t.Fatalf("read the scratch session zone: %v", err)
	}

	previous := database.Pool
	database.Pool = pool
	t.Cleanup(func() {
		database.Pool = previous
		if err := cleanup(); err != nil {
			t.Errorf("drop the scratch schema: %v", err)
		}
	})

	// Confirmed rather than assumed. A silently unpinned pool would make every assertion below pass
	// for the wrong reason, which is the failure mode a test written to control the environment is
	// most prone to. Only the non-UTC case is checked, because a pool that is still on UTC when UTC
	// was asked for is indistinguishable from a host that is already on UTC.
	if zone != "UTC" && got == "UTC" {
		t.Fatalf("the scratch pool reports UTC although %s was requested; the zone under test was "+
			"not applied, so the assertions below would pass without proving anything", zone)
	}
}

// TestActivityBucketsOnTheUTCDayUnderANonUTCSession is the heatmap half of the release's claim.
//
// A poem written at 20:00 UTC is already the next calendar day at +05:30. Without the explicit zone
// in the query, DATE(created_at) resolves in the session's zone and the work is credited to the
// following day -- so the heatmap would show activity on a day the author never wrote on, and
// MonthActivity for the correct month would omit it.
func TestActivityBucketsOnTheUTCDayUnderANonUTCSession(t *testing.T) {
	useSessionInZone(t, nonUTC)

	// 20:00 UTC on 10 March is 01:30 on 11 March at +05:30. The instant is the same either way; only
	// the calendar day it belongs to is in question.
	const utcDay = "2026-03-10"
	written := time.Date(2026, time.March, 10, 20, 0, 0, 0, time.UTC)
	if local := written.In(mustLoadLocation(t, nonUTC)); local.Day() != 11 {
		t.Fatalf("this test depends on 20:00 UTC falling on the next day at +05:30, but it is the "+
			"%dth there; the premise no longer holds", local.Day())
	}
	insertPoemAt(t, "Evening work", written)

	dates, err := services.MonthActivity(context.Background(),
		time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("services.MonthActivity: %v", err)
	}

	if len(dates) != 1 {
		t.Fatalf("MonthActivity returned %d date(s), want 1: the work belongs to %s UTC and to "+
			"no other day", len(dates), utcDay)
	}
	if got := dates[0].UTC().Format("2006-01-02"); got != utcDay {
		t.Fatalf("the work was bucketed to %s, want %s; a session at +05:30 resolved DATE() in its "+
			"own zone and moved the work onto the following day", got, utcDay)
	}
}

// TestStreakCountsTheUTCDayUnderANonUTCSession is the streak half.
//
// CurrentStreak counts back from clock.TodayUTC(), so the consumer is already UTC. If the query
// resolved the day in the session's zone instead, a poem written this evening UTC would read as
// belonging to tomorrow, would not match today, and a genuine streak would report zero. That is the
// most user-visible way this class of bug shows up: a streak that is real and reads as broken.
func TestStreakCountsTheUTCDayUnderANonUTCSession(t *testing.T) {
	useSessionInZone(t, nonUTC)

	today := clock.TodayUTC()
	// Late in the UTC day, so at +05:30 it is already tomorrow.
	insertPoemAt(t, "Tonight", time.Date(today.Year(), today.Month(), today.Day(), 20, 0, 0, 0, time.UTC))

	streak, err := services.CurrentStreak(context.Background())
	if err != nil {
		t.Fatalf("services.CurrentStreak: %v", err)
	}
	if streak != 1 {
		t.Fatalf("CurrentStreak = %d, want 1; a poem written at 20:00 UTC was read as belonging to "+
			"the following day because the session zone decided which day it was on", streak)
	}
}

// TestOrderingIsTotalWhenCreatedAtTies covers the other half of C4: a sort that is not a total order.
//
// Three works sharing one timestamp is not a contrived input. now() is a transaction timestamp, so
// every row written by one transaction carries the identical value, and a bulk insert or an import
// does exactly that. The assertion is made through pagination rather than by comparing one list to
// another, because OFFSET is where an unstable sort actually causes loss: page boundaries move, and a
// work can appear on two pages or on none.
func TestOrderingIsTotalWhenCreatedAtTies(t *testing.T) {
	useSessionInZone(t, "UTC")

	truncatePoems(t)
	tied := time.Date(2026, time.May, 4, 11, 0, 0, 0, time.UTC)
	const n = 5
	for i := 0; i < n; i++ {
		insertPoemAt(t, "tied work", tied)
	}

	// Walk the whole list one row at a time. Every row must be seen exactly once: an unstable order
	// shows up here as a repeat or a shortfall, which a single query cannot reveal.
	seen := map[string]int{}
	var order []string
	for offset := 0; offset < n; offset++ {
		page, err := services.ListPoems(context.Background(), 1, offset)
		if err != nil {
			t.Fatalf("ListPoems(limit 1, offset %d): %v", offset, err)
		}
		if len(page) != 1 {
			t.Fatalf("page at offset %d has %d row(s), want 1", offset, len(page))
		}
		seen[page[0].ID]++
		order = append(order, page[0].ID)
	}

	if len(seen) != n {
		t.Fatalf("paging one row at a time surfaced %d distinct work(s) out of %d; a tie in "+
			"created_at let rows be skipped or repeated across pages", len(seen), n)
	}
	for id, times := range seen {
		if times != 1 {
			t.Fatalf("work %s appeared on %d pages, want 1", id, times)
		}
	}

	// And the order has to be the same every time, not merely complete once.
	again := map[string]int{}
	for offset := 0; offset < n; offset++ {
		page, err := services.ListPoems(context.Background(), 1, offset)
		if err != nil {
			t.Fatalf("second pass, ListPoems(limit 1, offset %d): %v", offset, err)
		}
		again[page[0].ID]++
	}
	for id, times := range again {
		if seen[id] != times {
			t.Fatalf("work %s appeared on %d pages in the second pass and %d in the first; the "+
				"order is not stable", id, times, seen[id])
		}
	}

	// The documented order is created_at DESC, id DESC, so with every timestamp equal the sequence
	// must be strictly descending by id. Asserting the exact rule is what stops a future edit from
	// "fixing" the tiebreak into something stable but different.
	for i := 1; i < len(order); i++ {
		if order[i-1] <= order[i] {
			t.Fatalf("rows %d and %d of the tie are %s then %s; with equal created_at the order "+
				"must be id descending", i-1, i, order[i-1], order[i])
		}
	}
}

// mustLoadLocation resolves a zone name, failing the test if the database server does not know it.
func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()

	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("the test host does not know the zone %q: %v", name, err)
	}
	return loc
}
