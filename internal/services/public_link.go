package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/models"
)

// Public submission links are bearer capabilities, but they carry no session: anyone holding the
// URL may append one text to one published work. The service keeps the published gate and the
// revocation check in the same statements as the writes so a withdrawn link cannot be used.
var (
	ErrNoPublicLink  = errors.New("no such public link")
	ErrWorkNotPublic = errors.New("only a published work can receive public submissions")
)

// PublicLink is one author-created link for a published work.
type PublicLink struct {
	ID        string
	PoemID    string
	CreatedAt time.Time
	RevokedAt *time.Time
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

// CreatePublicLink mints a link for one published work and returns the raw token once.
func CreatePublicLink(ctx context.Context, poemID string) (token, id string, err error) {
	if err := database.Require(); err != nil {
		return "", "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate a public link token: %w", err)
	}
	token = hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	linkID := uuid.NewString()
	id = linkID

	var created time.Time
	err = database.Pool.QueryRow(ctx, fmt.Sprintf(`
        INSERT INTO public_links (id, poem_id, token_hash, created_at)
        SELECT $1, p.id, $2, now()
        FROM poems p
        WHERE p.id = $3 AND %s
        ON CONFLICT (poem_id) DO UPDATE SET
            id = EXCLUDED.id, token_hash = EXCLUDED.token_hash,
            created_at = EXCLUDED.created_at, revoked_at = NULL
        RETURNING created_at`, models.PublishedFilter),
		linkID, hex.EncodeToString(sum[:]), poemID).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrWorkNotPublic
	}
	if err != nil {
		return "", "", err
	}
	_ = created
	return token, id, nil
}

// ResolvePublicLink validates a token and returns its work.
func ResolvePublicLink(ctx context.Context, token string) (PublicLink, error) {
	var link PublicLink
	if token == "" {
		return link, ErrNoPublicLink
	}
	sum := sha256.Sum256([]byte(token))
	err := database.Pool.QueryRow(ctx, fmt.Sprintf(`
        SELECT l.id, l.poem_id, l.created_at, l.revoked_at
        FROM public_links l
        JOIN poems p ON p.id = l.poem_id
        WHERE l.token_hash = $1 AND l.revoked_at IS NULL AND %s`, models.PublishedFilter),
		hex.EncodeToString(sum[:])).Scan(&link.ID, &link.PoemID, &link.CreatedAt, &link.RevokedAt)
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
            WHERE l.id = $2 AND l.revoked_at IS NULL AND %s
        )
        RETURNING created_at`, models.PublishedFilter),
		submission.ID, link.ID, link.PoemID,
		submission.GuestName, submission.Content).Scan(&submission.CreatedAt)
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
        SELECT id, poem_id, created_at, revoked_at FROM public_links
        WHERE poem_id = $1 ORDER BY created_at DESC`, poemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PublicLink
	for rows.Next() {
		var link PublicLink
		if err := rows.Scan(&link.ID, &link.PoemID, &link.CreatedAt, &link.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

// RevokePublicLink withdraws a link and every submission path through it.
func RevokePublicLink(ctx context.Context, poemID, linkID string) error {
	tag, err := database.Pool.Exec(ctx, `
        UPDATE public_links SET revoked_at = now()
        WHERE id = $1 AND poem_id = $2 AND revoked_at IS NULL`, linkID, poemID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNoPublicLink
	}
	return nil
}

func isBlank(value string) bool {
	return len([]rune(value)) == 0
}
