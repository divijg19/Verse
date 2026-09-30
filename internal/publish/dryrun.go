// Package publish implements the author/viewer split's publication surface.
//
// It exists in v0.4.12 as the *enumeration* half of publication and nothing else. There is no
// cmd/publish, no public templ layer, and no code in this repository that writes to published_at --
// and that absence is the point, because the first step of an irreversible operation should be the
// one that can be run without consequences.
//
// The order the split is built in is chosen so the risky part is never first. D4's reasoning is that
// publication is hard to reverse: a redeploy removes the file, but search engines have cached it and
// inbound links persist. So this release provides the way to see exactly what publishing would expose,
// and stops. The write, and the static site that serves it, come after this has been run against a
// real library and read by the person whose work it is.
//
// The dry-run is non-destructive by construction rather than by convention: every function here takes
// a context and returns data, nothing opens a transaction, and nothing executes a statement that is
// not a SELECT. TestDryRunWritesNothing checks that from the database's own point of view rather than
// by trusting this comment.
package publish

import (
	"context"
	"time"

	"github.com/divijg19/Verse/internal/models"
	"github.com/divijg19/Verse/internal/presenters"
	"github.com/divijg19/Verse/internal/services"
)

// Entry is one work as the publisher would see it.
type Entry struct {
	// ID is the work's UUID, which is also its public URL path. It is stable for the life of the
	// work and is not derived from the text, so editing a work cannot change its address.
	ID string

	// Title is the work's first non-blank line, untruncated.
	//
	// Deliberately not presenters.WorkTitle, which elides to a display width. A listing has room to
	// show what a work is called, and an elided title here would be reported back to the author as
	// the work's name by the one tool whose purpose is to tell them what they are about to publish.
	// The narrow and wide widths exist to lay out a page; this is not a page.
	//
	// No "Untitled" fallback, unlike WorkTitle. A blank title in a dry-run is information -- it means
	// a row that is empty or whitespace, which validateContent should have prevented -- and silently
	// renaming it "Untitled" would hide exactly the oddity the author is looking for. Empty here means
	// empty, and TestDryRunReportsAnUntitleableWorkBlank makes that explicit.
	Title string

	// SizeBytes is the length of the work's content in bytes.
	//
	// Present because the author's decision needs it. A library of four thousand one-line notes and a
	// library of twelve long poems are very different things to put a name on the internet, and a
	// dry-run that prints four thousand identical short rows gives a reader no way to tell the two
	// apart before deciding.
	//
	// Byte length rather than rune count, because that is what a reader will download and what the
	// static site will serve. Computed in Go from the exact content the publisher would write, not
	// estimated in SQL, so the number reported is the number that would be deployed.
	SizeBytes int

	// CreatedAt is when the author wrote the work.
	CreatedAt time.Time

	// PublishedAt is when it became visible. Non-nil for every Entry, since entries only exist for
	// published works -- if this is nil, something has filtered incorrectly upstream.
	PublishedAt time.Time
}

// Result is what a dry-run reports.
type Result struct {
	// Entries are the works that would be published, in the publisher's own order.
	Entries []Entry

	// Count is len(Entries), carried explicitly because a slice length is the value a caller is least
	// likely to check and most likely to need.
	Count int

	// Counted is what the database reports for the same filter, read by a separate query.
	//
	// Kept rather than derived from Entries so a disagreement between the list and its count is
	// visible as a disagreement. These are the same predicate today; reading them separately is
	// exactly what makes a future limit on the list query show up as Counted != Count instead of as
	// a smaller site.
	Counted int
}

// Complete reports whether this enumeration is the entire published set.
//
// Named for the property rather than the symptom: an archive missing work is a site that looks
// complete and is not, and the failure is invisible until someone goes looking for a specific poem.
// The check exists so the caller has to ask.
func (r Result) Complete() bool {
	return r.Count == r.Counted
}

// reader is the shape dryRun needs, named so a test can supply a deliberately wrong one.
//
// The alternative is a *pgxpool.Pool and a query in this package, which would be a second copy of the
// publisher's query -- and the publisher's query is the security boundary of the split, so a second
// copy is precisely the duplication models.PublishedFilter exists to prevent. Injecting the reader
// keeps one definition of "published" in the codebase.
type reader func(context.Context) ([]models.Poem, error)

// countFn counts the same set. Injectable for the same reason, and because asserting that dryRun
// reports the database's count rather than its own slice length is only meaningful if the two are
// able to differ.
type countFn func(context.Context) (int, error)

// DryRun reports what publishing now would expose, and changes nothing.
//
// This is the gate in front of the irreversible half. It exists because D4 established that
// publication is the operation in this project that a redeploy cannot undo, and an operation you
// cannot undo should not be the first thing you run.
func DryRun(ctx context.Context) (Result, error) {
	return dryRun(ctx, services.ListPublishedPoems, services.CountPublishedPoems)
}

func dryRun(ctx context.Context, read reader, count countFn) (Result, error) {
	works, err := read(ctx)
	if err != nil {
		return Result{}, err
	}

	entries := make([]Entry, 0, len(works))
	for _, w := range works {
		// A work with a nil PublishedAt cannot reach here -- the filter excludes it -- but reading
		// the instant would panic rather than mislead, so the zero value is used and the invariant
		// is checked by the caller rather than trusted here. Cheap, and it turns a latent nil
		// dereference into a visibly wrong timestamp.
		var publishedAt time.Time
		if w.PublishedAt != nil {
			publishedAt = *w.PublishedAt
		}
		entries = append(entries, Entry{
			ID:          w.ID,
			Title:       presenters.FirstNonEmptyLine(w.Content),
			SizeBytes:   len(w.Content),
			CreatedAt:   w.CreatedAt,
			PublishedAt: publishedAt,
		})
	}

	// Read after the list, and not from the list, so truncation is detectable rather than
	// self-consistent.
	counted := len(entries)
	if count != nil {
		if counted, err = count(ctx); err != nil {
			return Result{}, err
		}
	}

	return Result{Entries: entries, Count: len(entries), Counted: counted}, nil
}
