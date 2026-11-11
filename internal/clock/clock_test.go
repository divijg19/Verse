package clock_test

import (
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/clock"
)

// The clock is the one place the application reads the wall clock, and these tests are what make
// it a seam rather than a wrapper. A clock that cannot be moved is a clock whose boundary cases
// cannot be tested, and the boundary is where the bugs are: a work written at 23:59 UTC, a streak
// that crosses a month, a day that has not started.

// TestNowUTCIsTheGivenInstantInUTC covers the replacement itself.
func TestNowUTCIsTheGivenInstantInUTC(t *testing.T) {
	instant := time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)

	restore := clock.SetNow(func() time.Time { return instant })
	defer restore()

	if got := clock.NowUTC(); !got.Equal(instant) {
		t.Errorf("NowUTC() = %v, want %v", got, instant)
	}
}

// TestNowUTCNormalizesTheZone covers the conversion.
//
// The instant is the same wherever it is read; the zone is how it is written down. A clock that
// returned the local zone would make every stored timestamp depend on where the service runs.
func TestNowUTCNormalizesTheZone(t *testing.T) {
	zone := time.FixedZone("test", 5*60*60+30*60)
	instant := time.Date(2026, time.March, 4, 12, 0, 0, 0, zone)

	restore := clock.SetNow(func() time.Time { return instant })
	defer restore()

	got := clock.NowUTC()
	if got.Location() != time.UTC {
		t.Errorf("NowUTC() is in %v, want UTC", got.Location())
	}
	if !got.Equal(instant) {
		t.Errorf("NowUTC() = %v, want the same instant as %v", got, instant)
	}
}

// TestTodayUTCIsMidnightOfTheUTCDay covers the truncation.
//
// It is midnight UTC rather than midnight anywhere else, and that is the difference the streak
// depends on: under a +05:30 session a work written at 20:00 UTC belongs to the current UTC day,
// and a "today" computed in the session's own zone would put it on tomorrow.
func TestTodayUTCIsMidnightOfTheUTCDay(t *testing.T) {
	zone := time.FixedZone("test", 5*60*60+30*60)
	// 20:00 UTC on the fourth, which is already the fifth in +05:30.
	instant := time.Date(2026, time.March, 4, 20, 0, 0, 0, zone)

	restore := clock.SetNow(func() time.Time { return instant })
	defer restore()

	got := clock.TodayUTC()
	want := time.Date(2026, time.March, 4, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("TodayUTC() = %v, want %v", got, want)
	}
}

// TestSetNowRestoresTheClock covers the cleanup.
//
// A test that moves the clock and does not put it back would make every test after it depend on
// the order they run in, which is the flakiness the seam exists to remove.
func TestSetNowRestoresTheClock(t *testing.T) {
	instant := time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)

	restore := clock.SetNow(func() time.Time { return instant })
	if got := clock.NowUTC(); !got.Equal(instant) {
		t.Fatalf("NowUTC() = %v, want the replacement %v", got, instant)
	}

	restore()

	// The real clock is back: it is within a second of now, which the replacement is not.
	real := clock.NowUTC()
	if real.Equal(instant) {
		t.Error("the clock was not restored; it is still the replacement")
	}
	if real.Sub(time.Now().UTC()) > time.Second || time.Now().UTC().Sub(real) > time.Second {
		t.Errorf("the restored clock reads %v, which is not close to now", real)
	}
}
