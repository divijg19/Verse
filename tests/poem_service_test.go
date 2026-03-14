package tests

import (
	"context"
	"testing"

	"github.com/divijg19/Verse/internal/services"
)

func TestPoemServiceListAndSearchExcludesDeleted(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	activeID := insertPoem(t, "Lantern across dark water\nDust in late sunlight")
	deletedID := insertPoem(t, "Lantern hidden by fog")
	markPoemDeleted(t, deletedID)

	poems, err := services.ListPoems(context.Background(), 100, 0)
	if err != nil {
		t.Fatalf("services.ListPoems failed: %v", err)
	}

	if len(poems) != 1 {
		t.Fatalf("ListPoems returned %d poems, want 1", len(poems))
	}
	if poems[0].ID != activeID {
		t.Fatalf("ListPoems first id = %q, want %q", poems[0].ID, activeID)
	}

	results, err := services.SearchPoems(context.Background(), "Lantern", 100, 0)
	if err != nil {
		t.Fatalf("services.SearchPoems failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("SearchPoems returned %d poems, want 1", len(results))
	}
	if results[0].ID != activeID {
		t.Fatalf("SearchPoems id = %q, want %q", results[0].ID, activeID)
	}
}
