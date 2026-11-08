package publish

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/models"
)

func loadTime(day int) time.Time {
	return time.Date(2026, time.March, day, 12, 0, 0, 0, time.UTC)
}

func loadTimePtr(day int) *time.Time {
	t := loadTime(day)
	return &t
}

func TestLoadRejectsAnIncompleteOrAmbiguousSet(t *testing.T) {
	t.Run("count mismatch", func(t *testing.T) {
		read := func(context.Context) ([]models.Poem, error) {
			return []models.Poem{{ID: "a", Content: "a", PublishedAt: loadTimePtr(1)}}, nil
		}
		count := func(context.Context) (int, error) { return 2, nil }

		if _, err := load(context.Background(), read, count); err == nil {
			t.Fatal("load accepted a list that disagrees with its count")
		}
	})

	t.Run("nil published at", func(t *testing.T) {
		read := func(context.Context) ([]models.Poem, error) {
			return []models.Poem{{ID: "a", Content: "a"}}, nil
		}
		count := func(context.Context) (int, error) { return 1, nil }

		if _, err := load(context.Background(), read, count); err == nil {
			t.Fatal("load accepted a published work without a publication instant")
		}
	})

	t.Run("duplicate ids", func(t *testing.T) {
		read := func(context.Context) ([]models.Poem, error) {
			return []models.Poem{
				{ID: "a", Content: "a", PublishedAt: loadTimePtr(1)},
				{ID: "a", Content: "b", PublishedAt: loadTimePtr(2)},
			}, nil
		}
		count := func(context.Context) (int, error) { return 2, nil }

		if _, err := load(context.Background(), read, count); err == nil {
			t.Fatal("load accepted two works with the same id")
		}
	})

	t.Run("stable order", func(t *testing.T) {
		read := func(context.Context) ([]models.Poem, error) {
			return []models.Poem{
				{ID: "older", Content: "older", PublishedAt: loadTimePtr(1)},
				{ID: "newer", Content: "newer", PublishedAt: loadTimePtr(3)},
				{ID: "tie-b", Content: "tie-b", PublishedAt: loadTimePtr(2)},
				{ID: "tie-a", Content: "tie-a", PublishedAt: loadTimePtr(2)},
			}, nil
		}
		count := func(context.Context) (int, error) { return 4, nil }

		works, err := load(context.Background(), read, count)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		got := []string{works[0].ID, works[1].ID, works[2].ID, works[3].ID}
		want := []string{"newer", "tie-b", "tie-a", "older"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	})

	t.Run("excerpt is bounded", func(t *testing.T) {
		read := func(context.Context) ([]models.Poem, error) {
			return []models.Poem{{
				ID:          "a",
				Content:     "A Title\n\n" + strings.Repeat("word ", 100),
				PublishedAt: loadTimePtr(1),
			}}, nil
		}
		count := func(context.Context) (int, error) { return 1, nil }

		works, err := load(context.Background(), read, count)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got := len([]rune(works[0].Excerpt)); got > ExcerptRunes {
			t.Errorf("excerpt is %d runes, want at most %d", got, ExcerptRunes)
		}
	})

	t.Run("untitled fallback", func(t *testing.T) {
		read := func(context.Context) ([]models.Poem, error) {
			return []models.Poem{{ID: "a", Content: "   \n\n", PublishedAt: loadTimePtr(1)}}, nil
		}
		count := func(context.Context) (int, error) { return 1, nil }

		works, err := load(context.Background(), read, count)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if works[0].Title != "Untitled" {
			t.Errorf("title = %q, want %q", works[0].Title, "Untitled")
		}
		if works[0].Path != "/w/a/" {
			t.Errorf("path = %q, want %q", works[0].Path, "/w/a/")
		}
	})
}
