// Package clock is the one place the application reads the wall clock.
//
// It exists because the time in this application is not only a timestamp. The dashboard counts a
// streak of days, the library groups works by day, and the heatmap buckets them by month, and all
// three of those answers depend on which day it is. Two of them depend on which day it is in UTC,
// which is a different question from which day it is locally, and the difference between the two is
// the bug this package was written to prevent.
//
// It also exists because a clock you cannot control is a clock you cannot test against. The seam
// below lets a test say what day it is, which is the only way to test the boundary cases: a work
// written at 23:59 UTC, a streak that crosses a month, a day that has not started yet.
//
// The seam is a variable rather than an interface because there is one implementation and one
// consumer. An interface here would be a second thing to keep in step for no gain.
package clock

import "time"

// now is the source of the current time.
//
// It is a variable so that a test can replace it, and it is unexported so that only SetNow and
// ResetNow -- which restore it -- can.
var now = time.Now

// NowUTC returns the current instant in UTC.
//
// Every timestamp the application stores or compares is in UTC, and the conversion happens here so
// that the reason is written down once rather than at each call site.
func NowUTC() time.Time {
	return now().UTC()
}

// TodayUTC returns the current UTC day, with the time of day truncated away.
//
// The truncation is to a UTC day rather than a local one, and that is the whole point: a session
// running in +05:30 that computed "today" locally would put a work written at 20:00 UTC on the
// following day, and the streak would report zero for a streak that is real.
//
// The truncation is on the UTC instant, so the result is midnight UTC.
func TodayUTC() time.Time {
	return NowUTC().Truncate(24 * time.Hour)
}

// SetNow replaces the source of the current time, and returns a function that restores it.
//
// It is for tests. The returned function is what puts the clock back, so a test that sets the clock
// cannot leave it moved for the next one.
//
// The replacement is not made thread-safe, and does not need to be: a test that sets the clock sets
// it before the work it is measuring begins, and the suite is sequential. A test that needs a clock
// that moves would be testing something else.
func SetNow(replacement func() time.Time) func() {
	previous := now
	now = replacement
	return func() { now = previous }
}

// ResetNow restores the real clock.
//
// It is SetNow(time.Now) with a name that says what it is for, and it exists so that a test's
// cleanup reads as what it is.
func ResetNow() {
	now = time.Now
}
