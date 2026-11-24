package models

import "time"

// Poem is the DB model used by services.
type Poem struct {
	ID        string
	Content   string
	CreatedAt time.Time

	// DeletedAt is the soft-delete instant, or nil while the work is live.
	//
	// A Poem does not say which query produced it, so the two optional instants below must be read
	// with the query in mind. Every read that returns a Poem is listed here; a field marked "nil" is
	// genuinely nil for that read, either because the filter guarantees it or because the query does
	// not select the column at all.
	//
	//	read                       deleted_at            published_at
	//	GetPoem                    nil (filtered)        not selected
	//	GetPublicLinkPoem          nil (filtered)        not selected
	//	ListPoems, SearchPoems     nil (filtered)        selected
	//	DashboardSummary.Latest    nil (filtered)        not selected
	//	GetPoemIncludingDeleted    selected              not selected
	//	ListDeletedPoems           selected ("dead")     not selected
	//	ListPublishedPoems         nil (filtered)        selected
	//	export's read              selected              selected
	//
	// The "selected" in ListDeletedPoems is deliberate: the recycle screen must distinguish a
	// deleted work from a live one, and an earlier version of that query omitted the column and
	// returned deleted works with a nil field. Counts are deliberately not written here -- this
	// table has gone stale twice already by naming numbers instead of reads.
	DeletedAt *time.Time

	// PublishedAt is the explicit publication instant, or nil while the work is a draft.
	//
	// The table above is the reason this field needs its own column: selecting a live work does not
	// tell you whether it is published. ListPoems used to omit published_at, and the Share picker
	// read the field from its rows, so every published work appeared as a draft and Copy Link was
	// disabled. The query now selects PublishedPoemColumns.
	//
	// A draft is not deleted and is not hidden. The authoring app filters on deleted_at only, so
	// every draft remains visible to the author exactly as before -- which is why the split needed
	// no status badge. Publication governs the publisher's view and the Share screen's Copy Link.
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
// Several queries in this repository read a poem, and most of them wrote the same three columns out
// by hand. That is not duplication in the harmless sense: the column list and the Scan that follows it
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

// ExportPoemColumns is every column the export writes, in the order the export scans them.
//
// The export is the only read that wants all three of the instant columns at once: the creation
// instant, the soft-delete instant and the publication instant. The first two were the whole of the
// schema once; the third arrived with the author/viewer split and the export was not taught about
// it, which made the export lossy in the one respect that mattered most -- it could not say which
// works were public.
//
// A separate constant rather than a variant flag on the others, because this list belongs to one
// caller and the others to several. A flag here would invite a scan that did not match the columns.
const ExportPoemColumns = PoemColumns + ", deleted_at, published_at"

// PublishedPoemColumns is PoemColumns plus published_at, for reads that need the publication state.
//
// Separate from the other two lists rather than a variant flag, because it is not about deletion:
// the publisher needs published_at itself, both to build a sitemap's lastmod and to report what it
// published and when, and the Share picker needs it to enable Copy Link only for published works.
// A reader that selected the three deletion-era columns would return every published work with no
// way to distinguish them or to date them.
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
