package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/linktoken"
	"github.com/divijg19/Verse/internal/models"
)

// Public submission links are bearer capabilities, but they carry no session: anyone holding the
// URL may append one text to one published work. The service keeps the published gate and the
// revocation check in the same statements as the writes so a withdrawn link cannot be used.
var (
	ErrNoPublicLink       = errors.New("no such public link")
	ErrWorkNotPublic      = errors.New("only a published work can receive public submissions")
	ErrLinkNotRecoverable = errors.New("the public link cannot be recovered")
	ErrPublicLinkRevoked  = errors.New("the public link was revoked")
)

// PublicLink is one author-created link for a published work.
type PublicLink struct {
	ID        string
	PoemID    string
	CreatedAt time.Time
	RevokedAt *time.Time

	// TokenSeed is NULL for links minted before v0.5.9 and cannot be scanned into a plain string.
	TokenSeed sql.NullString
	TokenHash string
}

// PublicSubmission is one appended text sent through a public link.
type PublicSubmission struct {
	ID        string
	LinkID    string
	PoemID    string
	GuestName string
	Content   string
	CreatedAt time.Time
}

// GetOrCreatePublicLink returns the work's public link when it is active and recoverable, minting
// one only when the work has none. The poem row and then the link row are locked in that order, so
// a concurrent click, rotate or revoke cannot make the returned URL dead on arrival. Reuse is a
// pure read: the token, seed and created_at stay untouched, and no holder is invalidated.
//
// There is one row per work (migrations/011). A revoked or unrecoverable row is never repaired here:
// that would silently change a capability already handed out. The explicit Rotate endpoint is the
// only repair path.
func GetOrCreatePublicLink(ctx context.Context, key linktoken.Key, poemID string) (token, id string, created bool, err error) {
	if err := database.Require(); err != nil {
		return "", "", false, err
	}

	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		return "", "", false, err
	}
	defer func() { discardRollback(tx.Rollback(ctx)) }()

	var probe int
	if err := tx.QueryRow(ctx, fmt.Sprintf(`
        SELECT 1 FROM poems WHERE id = $1 AND %s FOR UPDATE`, models.PublishedFilter), poemID).Scan(&probe); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", false, ErrWorkNotPublic
		}
		return "", "", false, err
	}

	var linkID, hash, seed string
	var revokedAt *time.Time
	var dbSeed sql.NullString
	err = tx.QueryRow(ctx, `
        SELECT id, token_hash, token_seed, revoked_at FROM public_links
        WHERE poem_id = $1 FOR UPDATE`, poemID).
		Scan(&linkID, &hash, &dbSeed, &revokedAt)
	if err == nil {
		if revokedAt != nil {
			return "", "", false, ErrPublicLinkRevoked
		}
		if !dbSeed.Valid {
			return "", "", false, ErrLinkNotRecoverable
		}
		token, err = linktoken.Recover(key, linktoken.Public, dbSeed.String, hash)
		if err != nil {
			return "", "", false, ErrLinkNotRecoverable
		}
		if err := tx.Commit(ctx); err != nil {
			return "", "", false, err
		}
		return token, linkID, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, err
	}

	seed, token, err = linktoken.Mint(key, linktoken.Public)
	if err != nil {
		return "", "", false, err
	}
	hash = linktoken.HashToken(token)
	linkID = uuid.NewString()
	tag, err := tx.Exec(ctx, fmt.Sprintf(`
        INSERT INTO public_links (id, poem_id, token_hash, token_seed, created_at)
        SELECT $1, p.id, $2, $3, now()
        FROM poems p
        WHERE p.id = $4 AND %s
        ON CONFLICT (poem_id) DO NOTHING`, models.PublishedFilter),
		linkID, hash, seed, poemID)
	if err != nil {
		return "", "", false, err
	}
	if tag.RowsAffected() == 0 {
		return "", "", false, errors.New("public link row disappeared during creation")
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", false, err
	}
	return token, linkID, true, nil
}

// RotatePublicLink replaces a public link's token in place, deliberately keeping the row id:
// public_submissions.link_id references it with no ON UPDATE CASCADE (migration 011), so changing the
// id once submissions exist raises a foreign-key violation and would orphan their attribution. The
// old token stops resolving because lookup is by token_hash. Submissions and created_at are preserved.
func RotatePublicLink(ctx context.Context, key linktoken.Key, poemID, linkID string) (token, id string, err error) {
	if err := database.Require(); err != nil {
		return "", "", err
	}

	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer func() { discardRollback(tx.Rollback(ctx)) }()

	var probe int
	if err := tx.QueryRow(ctx, fmt.Sprintf(`
        SELECT 1 FROM poems WHERE id = $1 AND %s FOR UPDATE`, models.PublishedFilter), poemID).Scan(&probe); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrWorkNotPublic
		}
		return "", "", err
	}

	seed, token, err := linktoken.Mint(key, linktoken.Public)
	if err != nil {
		return "", "", err
	}
	err = tx.QueryRow(ctx, `
        UPDATE public_links SET token_hash = $1, token_seed = $2, revoked_at = NULL
        WHERE id = $3 AND poem_id = $4
        RETURNING id`, linktoken.HashToken(token), seed, linkID, poemID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNoPublicLink
	}
	if err != nil {
		return "", "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return token, id, nil
}

// ResolvePublicLink validates a token and returns its work.
func ResolvePublicLink(ctx context.Context, token string) (PublicLink, error) {
	var link PublicLink
	if token == "" {
		return link, ErrNoPublicLink
	}
	err := database.Pool.QueryRow(ctx, fmt.Sprintf(`
        SELECT l.id, l.poem_id, l.created_at, l.revoked_at, l.token_hash
        FROM public_links l
        JOIN poems p ON p.id = l.poem_id
        WHERE l.token_hash = $1 AND l.revoked_at IS NULL AND %s`, models.PublishedFilter),
		linktoken.HashToken(token)).Scan(&link.ID, &link.PoemID, &link.CreatedAt, &link.RevokedAt, &link.TokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return link, ErrNoPublicLink
	}
	return link, err
}

// CreatePublicSubmission appends one text. The EXISTS clause repeats the published and revocation
// checks inside the write, so a link revoked after middleware ran cannot still accept a submission.
func CreatePublicSubmission(ctx context.Context, link PublicLink, name, content string) (PublicSubmission, error) {
	var submission PublicSubmission
	normalised, err := normalizeDraftContent(content)
	if err != nil {
		return submission, err
	}
	if isBlank(normalised) {
		return submission, ErrEmptyContent
	}

	submission.ID = uuid.NewString()
	submission.LinkID = link.ID
	submission.PoemID = link.PoemID
	submission.GuestName = cleanGuestNameForService(name)
	submission.Content = normalised

	err = database.Pool.QueryRow(ctx, fmt.Sprintf(`
        INSERT INTO public_submissions (id, link_id, poem_id, guest_name, content)
        SELECT $1, $2, $3, $4, $5
        WHERE EXISTS (
            SELECT 1 FROM public_links l
            JOIN poems p ON p.id = l.poem_id
            WHERE l.id = $2 AND l.token_hash = $6 AND l.revoked_at IS NULL AND %s
        )
        RETURNING created_at`, models.PublishedFilter),
		submission.ID, link.ID, link.PoemID,
		submission.GuestName, submission.Content, link.TokenHash).Scan(&submission.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return submission, ErrNoPublicLink
	}
	return submission, err
}

// ListPublicSubmissions returns a work's public feedback, newest first.
func ListPublicSubmissions(ctx context.Context, poemID string) ([]PublicSubmission, error) {
	rows, err := database.Pool.Query(ctx, `
        SELECT id, link_id, poem_id, guest_name, content, created_at
        FROM public_submissions WHERE poem_id = $1
        ORDER BY created_at DESC, id DESC`, poemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PublicSubmission
	for rows.Next() {
		var s PublicSubmission
		if err := rows.Scan(&s.ID, &s.LinkID, &s.PoemID, &s.GuestName, &s.Content, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountPublicSubmissionsByLink returns submission counts for the Share screen.
func CountPublicSubmissionsByLink(ctx context.Context, poemID string) (map[string]int, error) {
	rows, err := database.Pool.Query(ctx, `
        SELECT link_id, count(*)::bigint FROM public_submissions
        WHERE poem_id = $1 GROUP BY link_id`, poemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var linkID string
		var count int64
		if err := rows.Scan(&linkID, &count); err != nil {
			return nil, err
		}
		counts[linkID] = int(count)
	}
	return counts, rows.Err()
}

// ListPublicLinks returns the author's public links for one work.
func ListPublicLinks(ctx context.Context, poemID string) ([]PublicLink, error) {
	rows, err := database.Pool.Query(ctx, `
        SELECT id, poem_id, created_at, revoked_at, token_seed, token_hash FROM public_links
        WHERE poem_id = $1 ORDER BY created_at DESC, id DESC`, poemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PublicLink
	for rows.Next() {
		var link PublicLink
		if err := rows.Scan(&link.ID, &link.PoemID, &link.CreatedAt, &link.RevokedAt, &link.TokenSeed, &link.TokenHash); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

// RevokePublicLink withdraws a link and every submission path through it.
//
// It takes the poem row lock before the link update, in the same order as reuse and rotate, so a
// revoke cannot commit between the reuse read and its response and hand out a URL that is already
// dead. The instant comes from the application clock, as it does for private links, so one seam
// controls every expiry decision.
func RevokePublicLink(ctx context.Context, poemID, linkID string) error {
	if err := database.Require(); err != nil {
		return err
	}
	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { discardRollback(tx.Rollback(ctx)) }()

	var probe int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM poems WHERE id = $1 FOR UPDATE`, poemID).Scan(&probe); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoPublicLink
		}
		return err
	}

	tag, err := tx.Exec(ctx, `
        UPDATE public_links SET revoked_at = $3
        WHERE id = $1 AND poem_id = $2 AND revoked_at IS NULL`, linkID, poemID, clock.NowUTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNoPublicLink
	}
	return tx.Commit(ctx)
}

func isBlank(value string) bool {
	return len([]rune(value)) == 0
}
