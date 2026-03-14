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

// GroupAnimDelay returns a short stagger for each timeline group.
func GroupAnimDelay(groupIndex int) string {
	if groupIndex < 0 {
		groupIndex = 0
	}
	ms := groupIndex * 55
	if ms > 360 {
		ms = 360
	}
	return fmt.Sprintf("animation-delay:%dms;", ms)
}

// RowAnimDelay returns a staggered reveal delay for poem list rows.
func RowAnimDelay(groupIndex int, rowIndex int) string {
	if groupIndex < 0 {
		groupIndex = 0
	}
	if rowIndex < 0 {
		rowIndex = 0
	}

	ms := groupIndex*70 + rowIndex*28
	if ms > 520 {
		ms = 520
	}
	return fmt.Sprintf("animation-delay:%dms;", ms)
}
