package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
	"github.com/jackc/pgx/v5"
)

// DashboardHandler renders the dashboard surface. If the request is an HTMX request,
// it returns only the inner #screen content; otherwise it returns a full page.
func DashboardHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	month := parseDashboardMonth(r.URL.Query().Get("month"))

	total, _ := services.TotalPoems(ctx)
	current, _ := services.CurrentStreak(ctx)
	activeDates, _ := services.MonthActivity(ctx, month)
	days := buildHeatmapDays(month, activeDates)
	lastPoem := loadLastPoem(ctx)

	if isHeatmapRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := templ.Heatmap(month, days).Render(ctx, w); err != nil {
			http.Error(w, "failed to render heatmap", http.StatusInternalServerError)
		}
		return
	}

	renderSurface(w, r, "dashboard", templ.Dashboard(total, current, lastPoem, month, days))
}

func parseDashboardMonth(raw string) time.Time {
	if raw == "" {
		return services.NormalizeMonth(time.Now().UTC())
	}

	month, err := time.Parse("2006-01", raw)
	if err != nil {
		return services.NormalizeMonth(time.Now().UTC())
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

func loadLastPoem(ctx context.Context) *templ.LastPoemSummary {
	poem, err := services.LatestPoem(ctx)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return nil
	}

	title := dashboardFirstNonEmptyLine(poem.Content)
	if title == "" {
		title = "Untitled"
	}

	flat := strings.Join(strings.Fields(strings.ReplaceAll(poem.Content, "\n", " ")), " ")

	return &templ.LastPoemSummary{
		ID:        poem.ID,
		Title:     dashboardTruncateRunes(title, 72),
		Snippet:   dashboardTruncateRunes(flat, 120),
		CreatedAt: poem.CreatedAt,
	}
}

func dashboardFirstNonEmptyLine(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func dashboardTruncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
