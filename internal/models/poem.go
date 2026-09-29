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

// PoemColumns is the column list for a live work, in the order the scan functions expect.
//
// Seven queries in this repository read a poem, and six of them wrote the same three columns out by
// hand. That is not duplication in the harmless sense: the column list and the Scan that follows it
// must agree, and nothing tied them together. Adding a column to the table meant finding the scans
// by eye, and a scan that gained a column in the SQL but not in the destination would have been a
// runtime error in production rather than a compile error here.
//
// Kept as a plain string rather than generated because the repository has no code generation and one
// query interpolates it into a subquery. The Scan side stays explicit at each site, which is the
// point: this constant fixes which columns are read, not how they are stored.
const PoemColumns = "id, content, created_at"

// PoemColumnsIncludingDeleted is PoemColumns plus deleted_at.
//
// Separate rather than a flag on PoemColumns because the two are used for different purposes: the
// plain list is for reads that filter on deleted_at IS NULL and so can never see a non-nil value,
// while this one is for the recovery path and the archive, where the soft-delete instant is the
// answer being sought. See the DeletedAt comment on Poem.
const PoemColumnsIncludingDeleted = PoemColumns + ", deleted_at"
