package models

import "time"

// Poem is the DB model used by services.
type Poem struct {
	ID        string
	Content   string
	CreatedAt time.Time

	// DeletedAt is the soft-delete instant, or nil while the work is live.
	//
	// Five reads in this repository return a Poem, and only two of them populate this field. The
	// asymmetry is the trap, so here is all five rather than the three this comment used to name:
	//
	//	populated    GetPoemIncludingDeleted  -- no filter; the recovery path, where the instant is
	//	                                       the answer being sought
	//	nil, "live"  ListPoems, SearchPoems, GetPoem, DashboardSummary
	//	                                       -- all filter deleted_at IS NULL, so nil is the
	//	                                          correct value and always will be
	//	nil, "dead"  ListDeletedPoems
	//	                                       -- filters deleted_at IS NOT NULL. The column is
	//	                                          selected and scanned here precisely so that this
	//	                                          last row is a contradiction rather than a silent
	//	                                          inversion; it previously omitted deleted_at and
	//	                                          returned deleted works with a nil field.
	//
	// The two meanings are only distinguishable by the query, and nothing at the type level says
	// which one a given Poem carries. Before adding a sixth read, decide which row of that list it
	// belongs in.
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
// Seven queries in this repository read a poem, and five of them wrote the same three columns out by
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
