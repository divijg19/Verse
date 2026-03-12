package templ

import "time"

// DayActivity represents an activity marker for a single day.
type DayActivity struct {
    Date   time.Time
    Active bool
}
