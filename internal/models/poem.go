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
	//
	// The sixth read is ListPublishedPoems, and it belongs in no row of that table -- it is the
	// first read whose column is not a soft-delete. See PublishedAt.
	DeletedAt *time.Time

	// PublishedAt is the explicit publication instant, or nil while the work is a draft.
	//
	// This is the first field on Poem that is not about deletion, and it is the reason the
	// DeletedAt table above needed extending rather than leaving to become quietly wrong. Every read
	// that populates DeletedAt above is answering "is this live?", and there is no single answer to
	// the same question about a poem that is a draft: ListPublishedPoems returns rows with nil
	// DeletedAt and non-nil PublishedAt, a combination that appears in no row of that table.
	//
	// A draft is not deleted and is not hidden. The authoring app filters on deleted_at only, so
	// every draft remains visible to the author exactly as before -- which is why the split needed
	// no status badge, and why there is no listViewRow in that table. Publication governs the
	// publisher's view alone.
	PublishedAt *time.Time
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

// PublishedPoemColumns is PoemColumns plus published_at, for the publisher's read.
//
// Separate from the other two lists rather than a variant flag, because it is the only read that is
// not about deletion: the publisher needs published_at itself, both to build a sitemap's lastmod and
// to report what it published and when. A reader that selected the three deletion-era columns would
// return every published work with no way to distinguish them or to date them.
const PublishedPoemColumns = PoemColumns + ", published_at"

// PublishedFilter is the publisher's entire view of the library, as one literal.
//
// `published_at IS NOT NULL AND deleted_at IS NULL` -- and the two halves are not redundant. A work
// can be published and then soft-deleted, and a reader that checks only published_at would serve a
// deleted work, which is the one outcome the split exists to prevent. The order is the natural
// reading order and carries no performance claim: with the partial index on the live set, an
// additional published_at IS NOT NULL filter is a recheck on an already-narrowed scan.
//
// One literal because this predicate is the security boundary of the whole author/viewer split, and
// a boundary that appears in four places as four slightly different strings is a boundary with four
// chances to be wrong. It is not parameterised, for the same reason the live filter is not: no
// caller can turn it off.
//
// It is unparameterised text rather than a Go predicate, so the compiler cannot help here and only
// the test can. TestPublishedFilterExcludesDraftsAndDeletedWorks asserts the two exclusions
// separately, and TestListPublishedPoemsNeverTruncates asserts the query returns the whole set.
const PublishedFilter = "published_at IS NOT NULL AND deleted_at IS NULL"
