package templ

import "time"

// PoemView is a lightweight view model for rendering.
type PoemView struct {
	ID        string
	Content   string
	CreatedAt time.Time
	Title     string
	Snippet   string
}

// HeatmapDay represents a single day in the selected month.
type HeatmapDay struct {
	Date   time.Time
	Active bool
}

// LastPoemSummary is a small dashboard summary of the most recent poem.
type LastPoemSummary struct {
	ID        string
	Title     string
	Snippet   string
	CreatedAt time.Time
}

// PoemGroup groups poems by label (date label) for rendering.
type PoemGroup struct {
	Label string
	Poems []PoemView
}
