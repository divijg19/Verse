package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/models"
)

// ErrNotPublishable reports that a work cannot be published as it stands.
//
// It is a separate condition from ErrNotFound, because the two mean different things to the
// author. A work that does not exist is a stale link; a work that exists and cannot be published is
// a mistake in the request, and the author needs to know which it is.
var ErrNotPublishable = errors.New("not publishable")

// PublishPoem makes a work public.
//
// Publication is an explicit act by the author, and it is the only way a work reaches the public
// site. Nothing in this repository publishes anything by accident: the column has no default, the
// migration that added it left every existing row null, and the publisher's read is the only place
// the column is consulted.
//
// The work has to be live. A soft-deleted work cannot be published, because the publisher's filter
// excludes it -- published_at IS NOT NULL AND deleted_at IS NULL -- so publishing one would set a
// state the site would never show and the author would never be able to undo. The refusal is here
// rather than left to the reader of the filter because a state the application cannot reach is a
// state the application should not be able to create.
//
// The publication instant is the moment of publication, not the moment of writing. A work written
// in March and published in November is a November work to a reader, and the archive is ordered by
// when a reader met it.
func PublishPoem(ctx context.Context, id string) error {
	if err := database.Require(); err != nil {
		return err
	}

	now := clock.NowUTC()

	// The guard is in the statement rather than in a read first, so a work deleted between the
	// two cannot slip through.
	tag, err := database.Pool.Exec(ctx,
		`UPDATE poems SET published_at = $1 WHERE id = $2 AND deleted_at IS NULL`,
		now, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return classifyUnpublishable(ctx, id)
	}

	return nil
}

// UnpublishPoem takes a work back off the public site.
//
// The work's text is untouched, and the publication instant is cleared rather than the row
// deleted. Unpublishing is therefore undoable by publishing it again, and the history of the work is
// unaffected.
//
// A work that was never published reports ErrNotFound rather than succeeding quietly: the author
// asked to take something off the site that was never on it, and a silent success would say the
// opposite.
func UnpublishPoem(ctx context.Context, id string) error {
	if err := database.Require(); err != nil {
		return err
	}

	// The predicate is the constant rather than a second literal of it. The publisher's filter is
	// the security boundary of the whole split, and the repository has a test that refuses a second
	// spelling of it -- which is the right rule, because a second literal is a second place for a
	// draft to leak through.
	//
	// It also gives the operation the meaning it should have: a work is cleared only while it is
	// in the published set, so unpublishing a draft and unpublishing a withdrawn work both report
	// that there was nothing to withdraw.
	tag, err := database.Pool.Exec(ctx,
		`UPDATE poems SET published_at = NULL WHERE id = $1 AND `+models.PublishedFilter,
		id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	return nil
}

// classifyUnpublishable decides why a publication did not happen.
//
// A work that does not exist and a work that is soft-deleted are different problems, and the second
// one deserves a clearer answer than "not found": the author can restore a deleted work, and they
// cannot restore one that was never there.
func classifyUnpublishable(ctx context.Context, id string) error {
	_, err := GetPoemIncludingDeleted(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("look the work up: %w", err)
	}

	return fmt.Errorf("%w: the work is in the recycle bin", ErrNotPublishable)
}

// PublishedWork describes a work's publication state, for a surface that shows it.
type PublishedWork struct {
	// Published is whether the work is on the public site.
	Published bool

	// PublishedAt is when it was published, or nil while it is a draft.
	PublishedAt *time.Time
}

// PublicationState reports whether a work is published, and when.
//
// It is a separate read because the surfaces that show the state need both answers and the live
// reads do not carry the publication instant: they filter on it being null, and a nil there means
// "not published" rather than "unknown".
func PublicationState(ctx context.Context, id string) (PublishedWork, error) {
	if err := database.Require(); err != nil {
		return PublishedWork{}, err
	}

	var publishedAt *time.Time
	err := database.Pool.QueryRow(ctx,
		`SELECT published_at FROM poems WHERE id = $1`, id).Scan(&publishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishedWork{}, ErrNotFound
	}
	if err != nil {
		return PublishedWork{}, err
	}

	return PublishedWork{Published: publishedAt != nil, PublishedAt: publishedAt}, nil
}
