package templ

import "time"

// PoemView is a lightweight view model for rendering.
type PoemView struct {
	ID        string
	Content   string
	CreatedAt time.Time
	Title     string
	Snippet   string

	// Published is whether the work is on the public site.
	Published bool

	// PublishedAt is when it was published, or nil while it is a draft.
	PublishedAt *time.Time
}

// PublishAction is the value the publication form submits.
//
// It is "1" to publish and "0" to unpublish, which is the same shape as the flag the handler
// reads. The two halves of the toggle live together for the same reason the two do in the
// handler: a work is either on the public site or it is not.
func (v PoemView) PublishAction() string {
	if v.Published {
		return "0"
	}
	return "1"
}

// PublishActionLabel is the text on the publication button.
//
// It says what the button will do, not what the work is currently. A button labeled "Publish" on
// a published work reads as a claim rather than an action.
func (v PoemView) PublishActionLabel() string {
	if v.Published {
		return "Unpublish"
	}
	return "Publish"
}

// PublishButtonClass is the button's class.
//
// Publishing is the affirmative action and is the filled button; unpublishing is the withdrawal and
// is the quieter one. A screen with two equally loud buttons is a screen where neither is the
// obvious one.
func (v PoemView) PublishButtonClass() string {
	if v.Published {
		return "verse-button-secondary"
	}
	return "verse-button"
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
	PoemID   string
	Title    string
	Current  string
	Versions []PoemVersionView
	Restored string

	// DeletedAt reports whether the work is currently in the recycle.
	//
	// The screen cannot omit it. A soft-deleted work's /poem/{id} and /editor/{id} both resolve
	// through reads that filter deleted_at, so rendering those links unconditionally offers two
	// controls that 404 -- and the only reason v0.4.4 shipped them that way is that nothing knew
	// the state of the work being viewed.
	DeletedAt *time.Time
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
