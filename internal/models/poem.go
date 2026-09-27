package models

import "time"

// Poem is the DB model used by services.
type Poem struct {
	ID        string
	Content   string
	CreatedAt time.Time
}

// PoemVersion is one superseded revision of a poem's content.
//
// It records what the text was immediately before an edit, which is the only moment at which the
// previous text exists. A version is never modified: restoring one is performed as an ordinary edit,
// which records the restored-over text as a new version in turn.
type PoemVersion struct {
	ID         string
	PoemID     string
	Content    string
	RecordedAt time.Time
}
