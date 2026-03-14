package templ

import "fmt"

// TotalPoems returns the number of poem rows across all timeline groups.
func TotalPoems(groups []PoemGroup) int {
	total := 0
	for _, group := range groups {
		total += len(group.Poems)
	}
	return total
}

// PoemWord returns the singular/plural form for poem count labels.
func PoemWord(count int) string {
	if count == 1 {
		return "poem"
	}
	return "poems"
}

// LibrarySummary returns a compact summary for the archive header.
func LibrarySummary(query string, groups []PoemGroup) string {
	count := TotalPoems(groups)
	if query == "" {
		return fmt.Sprintf("%d %s", count, PoemWord(count))
	}
	if count == 0 {
		return fmt.Sprintf("No matches for \"%s\"", query)
	}
	return fmt.Sprintf("%d %s for \"%s\"", count, PoemWord(count), query)
}
