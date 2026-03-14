package tests

import (
	"context"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/services"
)

func TestMonthActivityIgnoresDeletedAndOtherMonths(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	insertPoemAt(t, "March lantern", time.Date(2026, time.March, 2, 10, 0, 0, 0, time.UTC))
	insertPoemAt(t, "March rain", time.Date(2026, time.March, 18, 8, 0, 0, 0, time.UTC))
	deletedID := insertPoemAt(t, "Deleted March ash", time.Date(2026, time.March, 9, 14, 0, 0, 0, time.UTC))
	insertPoemAt(t, "April harbor", time.Date(2026, time.April, 1, 9, 0, 0, 0, time.UTC))
	markPoemDeleted(t, deletedID)

	dates, err := services.MonthActivity(context.Background(), time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("services.MonthActivity failed: %v", err)
	}

	if len(dates) != 2 {
		t.Fatalf("MonthActivity returned %d dates, want 2", len(dates))
	}
	if dates[0].UTC().Format("2006-01-02") != "2026-03-02" {
		t.Fatalf("MonthActivity first date = %s, want 2026-03-02", dates[0].UTC().Format("2006-01-02"))
	}
	if dates[1].UTC().Format("2006-01-02") != "2026-03-18" {
		t.Fatalf("MonthActivity second date = %s, want 2026-03-18", dates[1].UTC().Format("2006-01-02"))
	}
}

func TestTotalPoemsExcludesDeleted(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	insertPoem(t, "Lantern across dark water")
	deletedID := insertPoem(t, "Lantern beneath ash")
	markPoemDeleted(t, deletedID)

	total, err := services.TotalPoems(context.Background())
	if err != nil {
		t.Fatalf("services.TotalPoems failed: %v", err)
	}
	if total != 1 {
		t.Fatalf("TotalPoems returned %d, want 1", total)
	}
}

func TestCurrentStreakIgnoresDeletedPoems(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	insertPoemAt(t, "Yesterday was real", today.AddDate(0, 0, -1).Add(12*time.Hour))
	deletedID := insertPoemAt(t, "Deleted today", today.Add(12*time.Hour))
	markPoemDeleted(t, deletedID)

	streak, err := services.CurrentStreak(context.Background())
	if err != nil {
		t.Fatalf("services.CurrentStreak failed: %v", err)
	}
	if streak != 0 {
		t.Fatalf("CurrentStreak returned %d, want 0 when only today's poem is deleted", streak)
	}
}

func TestLatestPoemReturnsMostRecentNonDeleted(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	insertPoemAt(t, "Older poem", time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC))
	deletedID := insertPoemAt(t, "Deleted latest poem", time.Date(2026, time.March, 4, 9, 0, 0, 0, time.UTC))
	activeID := insertPoemAt(t, "Newest active poem", time.Date(2026, time.March, 3, 9, 0, 0, 0, time.UTC))
	markPoemDeleted(t, deletedID)

	poem, err := services.LatestPoem(context.Background())
	if err != nil {
		t.Fatalf("services.LatestPoem failed: %v", err)
	}
	if poem.ID != activeID {
		t.Fatalf("LatestPoem returned id %q, want %q", poem.ID, activeID)
	}
}
