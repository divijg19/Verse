package models

import "time"

// Poem is the DB model used by services.
type Poem struct {
	ID        string
	Content   string
	CreatedAt time.Time

	// DeletedAt is set only by GetPoemIncludingDeleted.
	//
	// The other reads -- ListPoems, SearchPoems, GetPoem -- all filter on deleted_at IS NULL, so they
	// leave it nil, and nil is the correct value for them. That asymmetry is the trap: a nil here
	// means "not deleted" for those three and "not populated" for this one, and the two are only
	// distinguishable because the queries differ. It is worth knowing before adding a fifth read.
	DeletedAt *time.Time
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
