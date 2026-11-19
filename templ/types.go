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

// ShareWorkOption is one work in the Share screen's picker.
//
// It carries only what the picker needs: an identifier for the endpoint, a title for the label, and
// whether the work is published, because Copy Link is meaningless for a draft. The body is fetched
// from the endpoint when an action actually needs it rather than embedded in every option.
type ShareWorkOption struct {
	// ID is the work's identifier, used to fetch its share data.
	ID string

	// Title is the work's title, truncated to a width that fits a picker option.
	Title string

	// Published is whether the work has a public address.
	Published bool
}

// ProposalView is a guest's suggested rewrite as it appears in the author's history.
type ProposalView struct {
	ID        string
	PoemID    string
	GuestName string
	Content   string
	CreatedAt time.Time
	Applied   bool
}

// GuestEditorData is the one work a private-link holder is allowed to edit.
//
// It carries no navigation, no library, and no other work. The expiration is shown so the guest
// knows the boundary; the server enforces the same instant before every request.
type GuestEditorData struct {
	Title     string
	Content   string
	ExpiresAt time.Time
	Minutes   int
}

// PrivateLinkView is one link on the author's Share screen.
type PrivateLinkView struct {
	ID            string    `json:"id"`
	PoemID        string    `json:"poemID"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Revoked       bool      `json:"revoked"`
	Expired       bool      `json:"expired"`
	ProposalCount int       `json:"proposalCount"`
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
	PoemID    string
	Title     string
	Current   string
	Versions  []PoemVersionView
	Proposals []ProposalView
	Restored  string

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
