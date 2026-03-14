package templ

import "time"

func heatmapMonthLabel(month time.Time) string {
	return normalizedHeatmapMonth(month).Format("January 2006")
}

func heatmapMonthParam(month time.Time) string {
	return normalizedHeatmapMonth(month).Format("2006-01")
}

func heatmapPrevMonth(month time.Time) time.Time {
	return normalizedHeatmapMonth(month).AddDate(0, -1, 0)
}

func heatmapNextMonth(month time.Time) time.Time {
	return normalizedHeatmapMonth(month).AddDate(0, 1, 0)
}

func heatmapLeadingBlankDays(month time.Time) int {
	weekday := int(normalizedHeatmapMonth(month).Weekday())
	if weekday == 0 {
		return 6
	}
	return weekday - 1
}

func heatmapTrailingBlankDays(month time.Time, dayCount int) int {
	total := heatmapLeadingBlankDays(month) + dayCount
	const fullGridCells = 42
	if total >= fullGridCells {
		return 0
	}
	return fullGridCells - total
}

func normalizedHeatmapMonth(month time.Time) time.Time {
	utc := month.UTC()
	return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func heatmapCellClass(active bool) string {
	if active {
		return "verse-heatmap-cell verse-heatmap-cell-active"
	}
	return "verse-heatmap-cell verse-heatmap-cell-idle"
}
