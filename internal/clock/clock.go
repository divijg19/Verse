package clock

import "time"

var now = time.Now

func NowUTC() time.Time {
	return now().UTC()
}

func TodayUTC() time.Time {
	return NowUTC().Truncate(24 * time.Hour)
}
