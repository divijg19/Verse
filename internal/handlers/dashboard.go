package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/presenters"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/errgroup"
)

// DashboardHandler renders the dashboard surface. If the request is an HTMX request,
// it returns only the inner #screen content; otherwise it returns a full page.
func DashboardHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	month := parseDashboardMonth(r.URL.Query().Get("month"))

	if isHeatmapRequest(r) {
		activeDates, err := services.MonthActivity(ctx, month)
		if err != nil {
			http.Error(w, "failed to load heatmap", http.StatusInternalServerError)
			return
		}

		days := buildHeatmapDays(month, activeDates)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := templ.Heatmap(month, days).Render(ctx, w); err != nil {
			http.Error(w, "failed to render heatmap", http.StatusInternalServerError)
		}
		return
	}

	data, err := loadDashboardData(ctx, month)
	if err != nil {
		http.Error(w, "failed to load dashboard", http.StatusInternalServerError)
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

func loadDashboardData(ctx context.Context, month time.Time) (dashboardData, error) {
	group, groupCtx := errgroup.WithContext(ctx)
	var total int
	var currentStreak int
	var days []templ.HeatmapDay
	var lastPoem *templ.LastPoemSummary

	group.Go(func() error {
		value, err := services.TotalPoems(groupCtx)
		if err != nil {
			return err
		}
		total = value
		return nil
	})

	group.Go(func() error {
		value, err := services.CurrentStreak(groupCtx)
		if err != nil {
			return err
		}
		currentStreak = value
		return nil
	})

	group.Go(func() error {
		activeDates, err := services.MonthActivity(groupCtx, month)
		if err != nil {
			return err
		}
		days = buildHeatmapDays(month, activeDates)
		return nil
	})

	group.Go(func() error {
		value, err := loadLastPoem(groupCtx)
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

func loadLastPoem(ctx context.Context) (*templ.LastPoemSummary, error) {
	poem, err := services.LatestPoem(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	title := presenters.FirstNonEmptyLine(poem.Content)
	if title == "" {
		title = "Untitled"
	}

	flat := presenters.FlattenContent(poem.Content)

	return &templ.LastPoemSummary{
		ID:        poem.ID,
		Title:     presenters.TruncateRunes(title, 72),
		Snippet:   presenters.TruncateRunes(flat, 120),
		CreatedAt: poem.CreatedAt,
	}, nil
}
