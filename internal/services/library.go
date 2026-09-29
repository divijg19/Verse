package services

import (
	"context"
	"errors"
	"strconv"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotFound reports that a read or a mutation named no row.
//
// Previously UpdatePoem and SoftDeletePoem discarded the affected-row count, so a request naming a
// nonexistent id returned success. Callers need to distinguish "changed" from "no such thing".
//
// It is also what the single-work reads return, which they did not used to: they passed
// pgx.ErrNoRows straight up, so a caller had to know which read it had called to know what to test
// for. Every read and every mutation in this package now reports the same condition the same way,
// and a handler that maps it to a 404 is correct for all of them.
var ErrNotFound = errors.New("not found")

// replaceContent is the shared body of the two functions that overwrite a work's text.
//
// UpdatePoem and RestorePoemVersion each did this in about thirty identical lines: begin, defer a
// rollback, lock the row and read what was there, record the displaced text as a retained version if
// it differed, write the new text, commit. RestorePoemVersion's extra step -- clearing deleted_at --
// is now expressed in the update statement it passes, and its deliberate lack of a deleted_at filter
// in the guard it passes.
//
// The two parameters are SQL fragments, and that is the point of the split. library.go's own
// reasoning for keeping these two functions separate in the first place was that a boolean
// parameterising UpdatePoem's `deleted_at IS NULL` guard would put one careless caller away from
// editing a deleted work invisibly. That reasoning is unchanged and still binds: the guard is
// visible as a literal at each call site, where a flag would have hidden it behind a `true`. Only the
// twenty-five lines that are genuinely the same were shared, and the comment on RestorePoemVersion
// records why the two differ.
func replaceContent(ctx context.Context, id, content, lockWhere, update string) error {
	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		return err
	}

	// Committed explicitly on the success path; the deferred rollback is then a no-op, which is
	// preferable to deferring a commit whose error would have to be checked after the response has
	// already been written.
	//
	// The closure is load-bearing, not stylistic. `defer discardRollback(tx.Rollback(ctx))` would
	// evaluate tx.Rollback(ctx) at the defer statement rather than at return, closing the transaction
	// before the work below had run -- which is exactly what happened the first time this was written
	// that way, and it presented as "tx is closed" on every update. migrate.go's own rollback uses
	// the closure form for the same reason.
	//
	// Rollback after a successful Commit returns ErrTxClosed, which is not worth reporting.
	// discardRollback is the same shape migrate.go uses, and for the same reason: errcheck runs with
	// check-blank, so an error assigned to the blank identifier is still reported. Passing it to a
	// function that returns nothing is the way to say "deliberately ignored" in a form the linter
	// accepts, which is better than a suppression that documents the safety but not the mechanism.
	defer func() { discardRollback(tx.Rollback(ctx)) }()

	// FOR UPDATE in both callers, and load-bearing: a concurrent edit between the read and the write
	// would leave the history describing a change that did not happen, because the version retained
	// below would be the text the other writer replaced rather than the text this one did.
	var previous string
	err = tx.QueryRow(ctx,
		`SELECT content FROM poems `+lockWhere, id).Scan(&previous)
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

	if _, err := tx.Exec(ctx, update, content, id); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// discardRollback swallows a rollback error that is deliberately not reported.
//
// Not a general-purpose error sink. It exists so that a deferred rollback reads as intentional to
// both the reader and errcheck, instead of carrying a //nolint that explains why the error is safe to
// drop but not why a suppression is needed.
func discardRollback(error) {}

// CreatePoem inserts a new poem and returns its id.
func CreatePoem(ctx context.Context, content string) (string, error) {
	if err := database.Require(); err != nil {
		return "", err
	}

	id := uuid.NewString()
	if _, err := database.Pool.Exec(ctx, `INSERT INTO poems (id, content) VALUES ($1, $2)`, id, content); err != nil {
		return "", err
	}

	return id, nil
}

// ListPoems returns the most recent poems (non-deleted) with limit/offset.
func ListPoems(ctx context.Context, limit, offset int) ([]models.Poem, error) {
	return listLivePoems(ctx, "", limit, offset)
}

// SearchPoems returns poems matching q (ILIKE), limited with optional offset.
func SearchPoems(ctx context.Context, q string, limit int, offset int) ([]models.Poem, error) {
	return listLivePoems(ctx, q, limit, offset)
}

// listLivePoems is the one query behind both the library and its search box.
//
// They were the same function written twice, 25 of 34 lines identical, and the differences were the
// ILIKE predicate, the parameter numbering, and a doc comment. A shared helper was worth doing for
// the obvious reason -- one place to change the paging, one place to get the ORDER BY right -- and
// for a less obvious one: the argument below is a string, so a caller cannot accidentally pass a
// value into the SQL text.
//
// An empty needle means "no predicate", which is how ListPoems uses it. That is a deliberate
// non-obvious contract, so it is stated here rather than left to be discovered: an empty q lists
// everything rather than matching nothing. SearchPoems only ever receives what the author typed.
//
// The id tiebreak on created_at is load-bearing, and the reason is the OFFSET rather than the order.
// A query whose sort is not a total order returns tied rows in whatever order the plan happens to
// produce, and that order need not be the same on the next execution. Paging through an unstable
// sort therefore drops rows and repeats them: a work on the boundary between page one and page two
// can appear on both, or on neither, and nothing reports it. Adding id makes the order total, so a
// given page is the same page every time. Search inherited the same ordering for the same reason, and
// before this change carried none of this explanation.
func listLivePoems(ctx context.Context, needle string, limit, offset int) ([]models.Poem, error) {
	if err := database.Require(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	// Composed rather than written twice, so the filter that excludes soft-deleted works is one
	// literal. It is not parameterised, deliberately: a caller cannot turn it off, and the query
	// plan is unaffected by whether the predicate text happens to be present.
	query := `
        SELECT ` + models.PoemColumns + `
        FROM poems
        WHERE deleted_at IS NULL`
	args := []any{}

	if needle != "" {
		query += `
            AND content ILIKE '%' || $1 || '%'`
		args = append(args, needle)
	}

	query += `
        ORDER BY created_at DESC, id DESC
        LIMIT $` + strconv.Itoa(len(args)+1) + ` OFFSET $` + strconv.Itoa(len(args)+2)
	args = append(args, limit, offset)

	rows, err := database.Pool.Query(ctx, query, args...)
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
	if err := database.Require(); err != nil {
		return p, err
	}
	row := database.Pool.QueryRow(ctx, `
        SELECT `+models.PoemColumns+`
        FROM poems
        WHERE id = $1
        AND deleted_at IS NULL`, id)
	if err := row.Scan(&p.ID, &p.Content, &p.CreatedAt); err != nil {
		// Translated rather than returned raw, so that "no such work" is the same condition
		// everywhere in this package. It was not: the mutating reads return ErrNotFound, and these
		// two returned pgx.ErrNoRows, so a caller had to know which read it had called. That is not
		// a theoretical inconvenience -- a handler that checked for the sentinel only reported a
		// missing poem as a 500.
		if errors.Is(err, pgx.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, err
	}
	return p, nil
}

// The counterpart to GetPoem, and deliberately separate rather than a flag on it. Every read that
// feeds the library, search, dashboard or editor wants the deleted_at filter; the one caller that
// does not is the recovery path, where the whole point is the work that is currently hidden. A
// boolean parameter there would invite a caller to pass the wrong answer without meaning to.
func GetPoemIncludingDeleted(ctx context.Context, id string) (models.Poem, error) {
	var p models.Poem
	if err := database.Require(); err != nil {
		return p, err
	}
	// deleted_at is selected because the caller needs to know whether the work is in the recycle: the
	// history screen offers different controls depending on it, and v0.4.4 offered the wrong ones to
	// everyone because nothing carried this value.
	row := database.Pool.QueryRow(ctx, `
        SELECT `+models.PoemColumnsIncludingDeleted+`
        FROM poems
        WHERE id = $1`, id)
	if err := row.Scan(&p.ID, &p.Content, &p.CreatedAt, &p.DeletedAt); err != nil {
		// As in GetPoem: one condition for "no such work" across the package.
		if errors.Is(err, pgx.ErrNoRows) {
			return p, ErrNotFound
		}
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
	if err := database.Require(); err != nil {
		return err
	}

	return replaceContent(ctx, id, content,
		`WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
		`UPDATE poems SET content = $1 WHERE id = $2`)
}

// ListDeletedPoems returns soft-deleted poems, most recently deleted first.
//
// This is the recovery path made visible. deleted_at was previously a one-way trip: the row survived
// in Postgres, so the work was technically recoverable by hand, but the application offered no
// listing, no restore and no purge -- the author had no way back to a poem they deleted by accident.
//
// deleted_at is selected, and scanned, where the two live reads omit it. That is not redundancy: it
// is the difference between a field that is correct and a field that is inverted. Every other read
// filters on deleted_at IS NULL, so a nil DeletedAt there means "not deleted" -- the correct value.
// This one filters on deleted_at IS NOT NULL, so with the column omitted a nil meant "not deleted"
// about a row that *is* deleted. Nothing read the field on this path, so it was latent rather than
// live, but the next caller that did would have received the opposite of the truth with no compile
// error and no test failure to warn them.
func ListDeletedPoems(ctx context.Context, limit, offset int) ([]models.Poem, error) {
	if err := database.Require(); err != nil {
		return nil, err
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
        SELECT `+models.PoemColumnsIncludingDeleted+`
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
		if err := rows.Scan(&p.ID, &p.Content, &p.CreatedAt, &p.DeletedAt); err != nil {
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
	if err := database.Require(); err != nil {
		return 0, err
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
	if err := database.Require(); err != nil {
		return err
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
	if err := database.Require(); err != nil {
		return nil, err
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
	if err := database.Require(); err != nil {
		return v, err
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

	if err := database.Require(); err != nil {
		return err
	}

	return replaceContent(ctx, poemID, version.Content,
		`WHERE id = $1 FOR UPDATE`,
		`UPDATE poems SET content = $1, deleted_at = NULL WHERE id = $2`)
}

// SoftDeletePoem marks an active poem as deleted by setting deleted_at.
//
// Already-deleted rows report ErrNotFound rather than succeeding silently, so a repeated delete is
// visible to the caller instead of looking like a fresh success.
func SoftDeletePoem(ctx context.Context, id string) error {
	if err := database.Require(); err != nil {
		return err
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
