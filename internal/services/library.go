package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotFound reports that a mutation matched no row.
//
// Previously UpdatePoem and SoftDeletePoem discarded the affected-row count, so a request naming a
// nonexistent id returned success. Callers need to distinguish "changed" from "no such thing".
var ErrNotFound = errors.New("not found")

// CreatePoem inserts a new poem and returns its id.
func CreatePoem(ctx context.Context, content string) (string, error) {
	if database.Pool == nil {
		return "", fmt.Errorf("database not initialized")
	}

	id := uuid.NewString()
	if _, err := database.Pool.Exec(ctx, `INSERT INTO poems (id, content) VALUES ($1, $2)`, id, content); err != nil {
		return "", err
	}

	return id, nil
}

// ListPoems returns the most recent poems (non-deleted) with limit/offset.
func ListPoems(ctx context.Context, limit, offset int) ([]models.Poem, error) {
	if database.Pool == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := database.Pool.Query(ctx, `
        SELECT id, content, created_at
        FROM poems
        WHERE deleted_at IS NULL
        ORDER BY created_at DESC
        LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Poem
	for rows.Next() {
		var p models.Poem
		if err := rows.Scan(&p.ID, &p.Content, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SearchPoems returns poems matching q (ILIKE), limited with optional offset.
func SearchPoems(ctx context.Context, q string, limit int, offset int) ([]models.Poem, error) {
	if database.Pool == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := database.Pool.Query(ctx, `
        SELECT id, content, created_at
        FROM poems
        WHERE deleted_at IS NULL
        AND content ILIKE '%' || $1 || '%'
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3`, q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Poem
	for rows.Next() {
		var p models.Poem
		if err := rows.Scan(&p.ID, &p.Content, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetPoem returns a single poem by id if not deleted.
func GetPoem(ctx context.Context, id string) (models.Poem, error) {
	var p models.Poem
	if database.Pool == nil {
		return p, fmt.Errorf("database not initialized")
	}
	row := database.Pool.QueryRow(ctx, `
        SELECT id, content, created_at
        FROM poems
        WHERE id = $1
        AND deleted_at IS NULL`, id)
	if err := row.Scan(&p.ID, &p.Content, &p.CreatedAt); err != nil {
		return p, err
	}
	return p, nil
}

// GetPoemIncludingDeleted returns a poem by id whether or not it is soft-deleted.
//
// The counterpart to GetPoem, and deliberately separate rather than a flag on it. Every read that
// feeds the library, search, dashboard or editor wants the deleted_at filter; the one caller that
// does not is the recovery path, where the whole point is the work that is currently hidden. A
// boolean parameter there would invite a caller to pass the wrong answer without meaning to.
func GetPoemIncludingDeleted(ctx context.Context, id string) (models.Poem, error) {
	var p models.Poem
	if database.Pool == nil {
		return p, fmt.Errorf("database not initialized")
	}
	// deleted_at is selected because the caller needs to know whether the work is in the recycle: the
	// history screen offers different controls depending on it, and v0.4.4 offered the wrong ones to
	// everyone because nothing carried this value.
	row := database.Pool.QueryRow(ctx, `
        SELECT id, content, created_at, deleted_at
        FROM poems
        WHERE id = $1`, id)
	if err := row.Scan(&p.ID, &p.Content, &p.CreatedAt, &p.DeletedAt); err != nil {
		return p, err
	}
	return p, nil
}

// UpdatePoem updates the content of an existing active poem, retaining what it replaced.
//
// The prior content is recorded before the overwrite, in the same transaction as the overwrite
// itself. Two properties depend on that being one transaction rather than two statements:
//
//   - A failure between the insert and the update would leave a version row describing a change
//     that never happened, so the history would claim a revision that does not exist.
//   - A failure after the update but before a separate insert would destroy the previous text with
//     nothing recorded, which is the exact loss this exists to prevent.
//
// The row is locked FOR UPDATE before it is read, so two concurrent edits cannot both record the
// same text as their own starting point and lose one of the two changes between them.
//
// A no-op save records nothing. The previous text is identical to the new one, so nothing is at risk,
// and a history filled with one entry per autosave would be unreadable.
//
// The deleted_at guard matters: without it a soft-deleted work could be silently edited, and the
// edit would appear to succeed while remaining hidden.
func UpdatePoem(ctx context.Context, id string, content string) error {
	if database.Pool == nil {
		return fmt.Errorf("database not initialized")
	}

	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	// Committed explicitly on the success path; the deferred rollback is then a no-op, which is
	// preferable to deferring a commit whose error would have to be checked after the response has
	// already been written.
	// Rollback after a successful Commit is a no-op that returns ErrTxClosed, which is not worth
	// reporting. The error is discarded explicitly rather than ignored.
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // documented above

	var previous string
	err = tx.QueryRow(ctx,
		`SELECT content FROM poems WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if previous != content {
		if _, err := tx.Exec(ctx,
			`INSERT INTO poem_versions (id, poem_id, content) VALUES ($1, $2, $3)`,
			uuid.NewString(), id, previous); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE poems SET content = $1 WHERE id = $2`, content, id); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ListDeletedPoems returns soft-deleted poems, most recently deleted first.
//
// This is the recovery path made visible. deleted_at was previously a one-way trip: the row survived
// in Postgres, so the work was technically recoverable by hand, but the application offered no
// listing, no restore and no purge -- the author had no way back to a poem they deleted by accident.
func ListDeletedPoems(ctx context.Context, limit, offset int) ([]models.Poem, error) {
	if database.Pool == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	// Ordered by when it was deleted rather than when it was written, because the question this
	// screen answers is "what did I just lose".
	rows, err := database.Pool.Query(ctx, `
        SELECT id, content, created_at
        FROM poems
        WHERE deleted_at IS NOT NULL
        ORDER BY deleted_at DESC, id DESC
        LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Poem
	for rows.Next() {
		var p models.Poem
		if err := rows.Scan(&p.ID, &p.Content, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CountDeletedPoems reports how many works are in the recycle, which is not the number the listing
// shows.
//
// Exists so the recycle can say "100 of 143" instead of quietly truncating. A screen whose entire
// purpose is answering "did I lose it?" cannot hide 43 answers.
func CountDeletedPoems(ctx context.Context) (int, error) {
	if database.Pool == nil {
		return 0, fmt.Errorf("database not initialized")
	}
	var n int
	if err := database.Pool.QueryRow(ctx,
		`SELECT count(*) FROM poems WHERE deleted_at IS NOT NULL`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// RestorePoem clears deleted_at, returning a soft-deleted work to the library.
//
// Reports ErrNotFound for a poem that is not soft-deleted, mirroring SoftDeletePoem's treatment of
// an already-deleted row. That is what makes a repeated restore visible as a miss rather than
// looking like a fresh success, and it is the same reason a restore of an active poem cannot
// silently rewrite anything.
func RestorePoem(ctx context.Context, id string) error {
	if database.Pool == nil {
		return fmt.Errorf("database not initialized")
	}

	tag, err := database.Pool.Exec(ctx,
		`UPDATE poems SET deleted_at = NULL WHERE id = $1 AND deleted_at IS NOT NULL`, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPoemVersions returns a poem's superseded revisions, newest first.
//
// Deleted poems are included: their history is exactly what someone restoring an accidentally
// deleted work needs, and a soft-deleted poem is still a row that exists.
func ListPoemVersions(ctx context.Context, poemID string) ([]models.PoemVersion, error) {
	if database.Pool == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := database.Pool.Query(ctx, `
        SELECT id, poem_id, content, recorded_at
        FROM poem_versions
        WHERE poem_id = $1
        ORDER BY seq DESC`, poemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.PoemVersion
	for rows.Next() {
		var v models.PoemVersion
		if err := rows.Scan(&v.ID, &v.PoemID, &v.Content, &v.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetPoemVersion returns a single version belonging to the given poem.
//
// Scoped by poem_id as well as version id deliberately. An unscoped lookup would let a request name
// any version in the table and restore another poem's text into this one, which is a data-corrupting
// bug reachable by a mistyped id.
func GetPoemVersion(ctx context.Context, poemID, versionID string) (models.PoemVersion, error) {
	var v models.PoemVersion
	if database.Pool == nil {
		return v, fmt.Errorf("database not initialized")
	}
	row := database.Pool.QueryRow(ctx, `
        SELECT id, poem_id, content, recorded_at
        FROM poem_versions
        WHERE id = $1 AND poem_id = $2`, versionID, poemID)
	if err := row.Scan(&v.ID, &v.PoemID, &v.Content, &v.RecordedAt); err != nil {
		return v, err
	}
	return v, nil
}

// RestorePoemVersion makes a superseded revision the poem's current content, bringing a soft-deleted
// work back with it.
//
// If the work is currently in the recycle, the restore also clears deleted_at, in the same
// transaction. That is deliberate rather than incidental: the history screen is explicitly a recovery
// surface, so "restore this draft" reasonably means "bring this work back, with this text". Before
// this, a deleted work's history rendered a Restore button that was guaranteed to fail, because the
// update it routed through refuses to touch a soft-deleted row.
//
// This does NOT reuse UpdatePoem, and the duplication is the point. UpdatePoem's
// `deleted_at IS NULL` guard is load-bearing: it is what stops a soft-deleted work being silently
// edited while remaining hidden, so a restore that edits it would leave no record. Parameterising
// UpdatePoem with a flag that weakens the guard would put one careless caller away from editing
// deleted work invisibly. The version-insert statement is shared; the guard deliberately is not.
//
// Two properties follow from doing it in one transaction. The work is never visible in the library
// with the deleted text, not even momentarily. And the restore stays undoable, because the text it
// replaced is recorded as a new version -- including when the work was deleted, so a restore can be
// undone back to "deleted, with the bad paste".
//
// Undeleting is not itself versioned, because it changes no content. The history records what the
// work said, not whether it was filed.
func RestorePoemVersion(ctx context.Context, poemID, versionID string) error {
	version, err := GetPoemVersion(ctx, poemID, versionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if database.Pool == nil {
		return fmt.Errorf("database not initialized")
	}

	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // after Commit this is ErrTxClosed

	// No deleted_at filter, and FOR UPDATE for the same reason UpdatePoem locks: a concurrent edit
	// between the read and the write would leave the history describing a change that did not happen.
	var previous string
	err = tx.QueryRow(ctx,
		`SELECT content FROM poems WHERE id = $1 FOR UPDATE`, poemID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if previous != version.Content {
		if _, err := tx.Exec(ctx,
			`INSERT INTO poem_versions (id, poem_id, content) VALUES ($1, $2, $3)`,
			uuid.NewString(), poemID, previous); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE poems SET content = $1, deleted_at = NULL WHERE id = $2`,
		version.Content, poemID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// SoftDeletePoem marks an active poem as deleted by setting deleted_at.
//
// Already-deleted rows report ErrNotFound rather than succeeding silently, so a repeated delete is
// visible to the caller instead of looking like a fresh success.
func SoftDeletePoem(ctx context.Context, id string) error {
	if database.Pool == nil {
		return fmt.Errorf("database not initialized")
	}

	tag, err := database.Pool.Exec(ctx,
		`UPDATE poems SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
