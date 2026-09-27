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

// PoemVersionView is one superseded revision, as rendered in a poem's history.
type PoemVersionView struct {
	ID         string
	PoemID     string
	Content    string
	Title      string
	RecordedAt time.Time
}

// PoemHistory is a poem's history screen: the work itself, and what it used to say.
type PoemHistory struct {
	PoemID     string
	Title      string
	Current    string
	Versions   []PoemVersionView
	Restored   string
	RestoredID string
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
