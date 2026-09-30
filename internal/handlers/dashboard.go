package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/models"
	"github.com/divijg19/Verse/internal/presenters"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
	"golang.org/x/sync/errgroup"
)

// DashboardHandler renders the dashboard surface. If the request is an HTMX request,
// it returns only the inner #screen content; otherwise it returns a full page.
func DashboardHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	month := parseDashboardMonth(r.URL.Query().Get("month"))

	if isHeatmapRequest(r) {
		// The same query the full dashboard uses, narrowed to the month.
		//
		// This path used to call a month-only query of its own, which was one acquisition and
		// perfectly reasonable in isolation. Keeping it meant two ways to answer "which days in this
		// month have work on them", and two ways is how a streak and a heatmap end up disagreeing
		// about a day -- which is exactly what happened once, when one bucketed on the session's zone
		// and the other did not. Reading the shared window costs a few hundred short values on a
		// request a user makes by clicking a month arrow, and buys a single definition of a day.
		activeDates, err := services.ActivityDays(ctx, month)
		if err != nil {
			fail500(w, r, "failed to load heatmap", err)
			return
		}

		days := buildHeatmapDays(month, services.MonthDays(activeDates, month))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := templ.Heatmap(month, days).Render(ctx, w); err != nil {
			fail500(w, r, "failed to render heatmap", err)
		}
		return
	}

	data, err := loadDashboardData(ctx, month)
	if err != nil {
		fail500(w, r, "failed to load dashboard", err)
		return
	}

	renderSurface(w, r, "dashboard", templ.Dashboard(data.total, data.currentStreak, data.lastPoem, month, data.days))
}

func parseDashboardMonth(raw string) time.Time {
	if raw == "" {
		return services.NormalizeMonth(clock.NowUTC())
	}

	month, err := time.Parse("2006-01", raw)
	if err != nil {
		return services.NormalizeMonth(clock.NowUTC())
	}

	return services.NormalizeMonth(month)
}

func buildHeatmapDays(month time.Time, activeDates []time.Time) []templ.HeatmapDay {
	monthStart := services.NormalizeMonth(month)
	monthEnd := monthStart.AddDate(0, 1, 0)
	dayCount := monthEnd.AddDate(0, 0, -1).Day()

	activeSet := map[string]struct{}{}
	for _, d := range activeDates {
		activeSet[d.UTC().Format("2006-01-02")] = struct{}{}
	}

	days := make([]templ.HeatmapDay, 0, dayCount)
	for day := monthStart; day.Before(monthEnd); day = day.AddDate(0, 0, 1) {
		_, active := activeSet[day.Format("2006-01-02")]
		days = append(days, templ.HeatmapDay{Date: day, Active: active})
	}

	return days
}

func isHeatmapRequest(r *http.Request) bool {
	if !isHXRequest(r) {
		return false
	}

	target := r.Header.Get("HX-Target")
	if target == "" {
		target = r.Header.Get("Hx-Target")
	}

	return target == "heatmap" || target == "#heatmap"
}

type dashboardData struct {
	total         int
	currentStreak int
	lastPoem      *templ.LastPoemSummary
	days          []templ.HeatmapDay
}

// loadDashboardData fetches everything the dashboard shows in two pool acquisitions.
//
// It was four, run concurrently, and the pool has five connections. One dashboard load plus one
// health probe was therefore the whole pool: the probe's connection request queued behind a page view,
// and there is no AcquireTimeout to cap the wait, so a slow database turned the probe into the last
// caller served. The probe then fails, and the platform restarts the instance into the same slow
// database.
//
// Two acquisitions leave three, which is the point. It is two rather than three because the streak and
// the heatmap were the same query with different bounds and are now one query with the earlier of the
// two starts, bucketed in Go -- so they are also known to agree about which day a work belongs to,
// which two independent queries never guaranteed.
func loadDashboardData(ctx context.Context, month time.Time) (dashboardData, error) {
	group, groupCtx := errgroup.WithContext(ctx)
	var total int
	var currentStreak int
	var days []templ.HeatmapDay
	var lastPoem *templ.LastPoemSummary

	// The days feed both the streak and the heatmap, so they are read once and shared. Everything the
	// first goroutine writes happens before Wait returns, and every read of those variables is after
	// it, so the race detector is what keeps that from being a promise rather than a fact.
	group.Go(func() error {
		value, err := services.ActivityDays(groupCtx, month)
		if err != nil {
			return err
		}
		currentStreak = services.StreakFromDays(value, clock.TodayUTC())
		days = buildHeatmapDays(month, services.MonthDays(value, month))
		return nil
	})

	group.Go(func() error {
		summary, err := services.DashboardSummary(groupCtx)
		if err != nil {
			return err
		}
		total = summary.Total
		if summary.Latest == nil {
			return nil
		}
		value, err := lastPoemSummary(*summary.Latest)
		if err != nil {
			return err
		}
		lastPoem = value
		return nil
	})

	if err := group.Wait(); err != nil {
		return dashboardData{}, err
	}

	return dashboardData{
		total:         total,
		currentStreak: currentStreak,
		lastPoem:      lastPoem,
		days:          days,
	}, nil
}

// lastPoemSummary turns the most recent work into what the dashboard renders for it.
func lastPoemSummary(poem models.Poem) (*templ.LastPoemSummary, error) {
	flat := presenters.FlattenContent(poem.Content)

	return &templ.LastPoemSummary{
		ID:        poem.ID,
		Title:     presenters.WorkTitle(poem.Content, presenters.TitleWidthNarrow),
		Snippet:   presenters.TruncateRunes(flat, 120),
		CreatedAt: poem.CreatedAt,
	}, nil
}
