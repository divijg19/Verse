package handlers

import (
	"net/http"
	"time"

	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// DashboardHandler renders the dashboard surface. If the request is an HTMX request,
// it returns only the inner #screen content; otherwise it returns a full page.
func DashboardHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	total, _ := services.TotalPoems(ctx)
	current, _ := services.CurrentStreak(ctx)
	longest, _ := services.LongestStreak(ctx)
	activeDates, _ := services.ActivityLast30Days(ctx)

	// Build a set of active dates for quick lookup
	activeSet := map[string]struct{}{}
	for _, d := range activeDates {
		activeSet[d.UTC().Format("2006-01-02")] = struct{}{}
	}

	// Last 30 days, older -> newer
	now := time.Now().UTC().Truncate(24 * time.Hour)
	days := make([]templ.DayActivity, 0, 30)
	for i := 29; i >= 0; i-- {
		d := now.AddDate(0, 0, -i)
		_, active := activeSet[d.Format("2006-01-02")]
		days = append(days, templ.DayActivity{Date: d, Active: active})
	}

	renderSurface(w, r, "dashboard", templ.Dashboard(total, current, longest, days))
}
