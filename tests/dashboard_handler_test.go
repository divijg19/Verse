package tests

import (
	"strings"
	"testing"
	"time"
)

func TestDashboardInvalidMonthFallsBackToCurrentMonth(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/dashboard?month=not-a-month", nil)
	if status != 200 {
		t.Fatalf("GET /dashboard invalid month status = %d, want 200", status)
	}

	expectedLabel := time.Now().UTC().Format("January 2006")
	if !strings.Contains(body, expectedLabel) {
		t.Fatalf("dashboard fallback missing current month label %q in %q", expectedLabel, body)
	}
}

func TestDashboardHeatmapHTMXReturnsFragmentWithMonthNavigation(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	insertPoemAt(t, "March poem", time.Date(2026, time.March, 12, 9, 0, 0, 0, time.UTC))

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/dashboard?month=2026-03", map[string]string{
		"HX-Request": "true",
		"HX-Target":  "heatmap",
	})
	if status != 200 {
		t.Fatalf("GET /dashboard heatmap status = %d, want 200", status)
	}

	if !strings.Contains(body, `id="heatmap"`) {
		t.Fatalf("heatmap fragment missing wrapper: %q", body)
	}
	if !strings.Contains(body, "March 2026") {
		t.Fatalf("heatmap fragment missing selected month heading: %q", body)
	}
	if !strings.Contains(body, `/dashboard?month=2026-02`) || !strings.Contains(body, `/dashboard?month=2026-04`) {
		t.Fatalf("heatmap fragment missing month navigation links: %q", body)
	}
	if strings.Contains(body, `id="screen"`) {
		t.Fatalf("heatmap fragment unexpectedly rendered full screen wrapper: %q", body)
	}
}

func TestDashboardShowsLastPoemSummary(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	insertPoemAt(t, "First lantern line\nA second quieter line", time.Date(2026, time.March, 12, 9, 0, 0, 0, time.UTC))

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/dashboard", nil)
	if status != 200 {
		t.Fatalf("GET /dashboard status = %d, want 200", status)
	}
	if !strings.Contains(body, "Last Poem") {
		t.Fatalf("dashboard missing last poem label: %q", body)
	}
	if !strings.Contains(body, "First lantern line") {
		t.Fatalf("dashboard missing last poem title: %q", body)
	}
	if !strings.Contains(body, `/poem/`) {
		t.Fatalf("dashboard missing last poem link: %q", body)
	}
}
