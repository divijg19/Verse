package tests

import (
	"strings"
	"testing"
	"time"
)

func TestLibraryShowsTimelineGroupsAndSearchFragment(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	insertPoemAt(t, "Lantern across dark water\nDust in late sunlight", today.Add(10*time.Hour))
	insertPoemAt(t, "A quiet harbor", today.AddDate(0, 0, -1).Add(11*time.Hour))

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/library", nil)
	if status != 200 {
		t.Fatalf("GET /library status = %d, want 200", status)
	}

	todayIdx := strings.Index(body, "Today")
	yesterdayIdx := strings.Index(body, "Yesterday")
	if todayIdx == -1 || yesterdayIdx == -1 {
		t.Fatalf("library missing timeline labels: %q", body)
	}
	if todayIdx > yesterdayIdx {
		t.Fatalf("library timeline order incorrect: Today index %d, Yesterday index %d", todayIdx, yesterdayIdx)
	}
	if !strings.Contains(body, "Lantern across dark water") || !strings.Contains(body, "A quiet harbor") {
		t.Fatalf("library missing expected poem titles: %q", body)
	}

	status, body, _ = get(t, srv.URL+"/poems?q=Lantern", map[string]string{"HX-Request": "true"})
	if status != 200 {
		t.Fatalf("GET /poems search status = %d, want 200", status)
	}
	if !strings.Contains(body, "Lantern across dark water") {
		t.Fatalf("search fragment missing matching poem: %q", body)
	}
	if strings.Contains(body, "A quiet harbor") {
		t.Fatalf("search fragment includes non-matching poem: %q", body)
	}
	if strings.Contains(body, "<!DOCTYPE html>") || strings.Contains(body, `id="screen"`) {
		t.Fatalf("search endpoint returned full page instead of fragment: %q", body)
	}
}

func TestLibraryEmptyStates(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/library", nil)
	if status != 200 {
		t.Fatalf("GET /library empty state status = %d, want 200", status)
	}
	if !strings.Contains(body, "This library is empty.") {
		t.Fatalf("empty library missing archive message: %q", body)
	}
	if !strings.Contains(body, "Begin writing, and your poems will gather here.") {
		t.Fatalf("empty library missing supporting copy: %q", body)
	}

	status, body, _ = get(t, srv.URL+"/poems?q=missing", map[string]string{"HX-Request": "true"})
	if status != 200 {
		t.Fatalf("GET /poems empty search state status = %d, want 200", status)
	}
	if !strings.Contains(body, "No poems match this search.") {
		t.Fatalf("empty search missing search message: %q", body)
	}
}
