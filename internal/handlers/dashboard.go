package handlers

import (
	"bytes"
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

    var buf bytes.Buffer
    if err := templ.Dashboard(total, current, longest, days).Render(ctx, &buf); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // If HTMX request, return only inner HTML for #screen
    if r.Header.Get("HX-Request") == "true" || r.Header.Get("Hx-Request") == "true" {
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        w.Write(buf.Bytes())
        return
    }

    // Otherwise return a full page wrapping the screen content and global navigation.
    page := "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>Verse</title><link rel=\"stylesheet\" href=\"/static/css/output.css\"><script src=\"https://unpkg.com/htmx.org\"></script><style>#nav-top{position:fixed;top:24px;left:50%;transform:translateX(-50%);}#nav-left{position:fixed;left:24px;top:50%;transform:translateY(-50%);}#nav-right{position:fixed;right:24px;top:50%;transform:translateY(-50%);}#nav-bottom{position:fixed;bottom:24px;left:50%;transform:translateX(-50%);}</style></head><body class=\"bg-neutral-950 text-neutral-200 min-h-screen\"><div id=\"viewport\" class=\"min-h-screen flex items-center justify-center\">" +
        "<div id=\"nav-top\"><button hx-get=\"/caelum\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Caelum\" title=\"Caelum inspiration\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">▲ <span class=\"text-xs block\">Caelum</span></button></div>" +
        "<div id=\"nav-left\"><button hx-get=\"/dashboard\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Dashboard\" title=\"Dashboard\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">◀ <span class=\"text-xs block\">Dashboard</span></button></div>" +
        "<div id=\"nav-right\"><button hx-get=\"/library\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Library\" title=\"Library\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">▶ <span class=\"text-xs block\">Library</span></button></div>" +
        "<div id=\"nav-bottom\"><button hx-get=\"/share\" hx-target=\"#screen\" hx-swap=\"innerHTML\" aria-label=\"Navigate to Share\" title=\"Share\" class=\"px-3 py-1 rounded hover:bg-neutral-900\">▼ <span class=\"text-xs block\">Share</span></button></div>" +
        "<div id=\"screen\" class=\"max-w-3xl w-full transition-all duration-200 ease-out p-8\">" + buf.String() + "</div></div><script src=\"/static/js/navigation.js\"></script></body></html>"
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.Write([]byte(page))
}
