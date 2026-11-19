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

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/database"
)

// PrivateLinkTTL is how long a link may be opened and a proposal sent.
//
// Eight hours, matching the author's own session: a proposal is a short conversation about one
// draft, not an account. There is deliberately no renewal. A longer window would leave a capability
// to edit a work alive long after the author had forgotten it was handed out.
const PrivateLinkTTL = 8 * time.Hour

// ErrLinkExpired, ErrLinkRevoked and ErrNoLink distinguish why a link could not be used.
//
// They are separate because the person following a link is told something different from the
// person who revoked it, and because the handler can log a revoked link without mistaking it for a
// mistyped URL.
var (
	ErrLinkExpired = errors.New("the private link has expired")
	ErrLinkRevoked = errors.New("the private link was revoked")
	ErrNoLink      = errors.New("no such private link")
	ErrNoProposal  = errors.New("no such proposal")
)

// PrivateLink is one capability the author has handed out.
type PrivateLink struct {
	ID        string
	PoemID    string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// PrivateLinkSession is the scoped identity a link grants to its holder.
type PrivateLinkSession struct {
	// LinkID identifies the capability that was used.
	LinkID string

	// PoemID is the one work this session may touch.
	PoemID string

	// ExpiresAt is when the session stops working.
	ExpiresAt time.Time
}

// WorkProposal is a guest's suggested rewrite, kept until the author applies or discards it.
type WorkProposal struct {
	ID        string
	PoemID    string
	LinkID    string
	Branch    string
	GuestName string
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
	AppliedAt *time.Time
}

// CreatePrivateLink mints a link for one work and returns the raw token once.
//
// The raw token is returned to the caller and never stored. Only its SHA-256 is written, so a backup
// or a read of the table cannot be used to open the link. 256 bits from crypto/rand make guessing it
// infeasible, which is what lets the lookup be a hash comparison over an indexed column.
func CreatePrivateLink(ctx context.Context, poemID string) (token string, expiresAt time.Time, err error) {
	if err := database.Require(); err != nil {
		return "", time.Time{}, err
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("generate a private link token: %w", err)
	}

	token = hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])

	now := clock.NowUTC()
	expiresAt = now.Add(PrivateLinkTTL)
	id, err := uuid.NewRandom()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generate a private link id: %w", err)
	}

	tag, err := database.Pool.Exec(ctx, `
        INSERT INTO private_links (id, poem_id, token_hash, created_at, expires_at)
        SELECT $1, $2, $3, $4, $5
        WHERE EXISTS (SELECT 1 FROM poems WHERE id = $2 AND deleted_at IS NULL)`,
		id, poemID, hash, now, expiresAt)
	if err != nil {
		return "", time.Time{}, err
	}
	if tag.RowsAffected() == 0 {
		return "", time.Time{}, ErrNotFound
	}

	// The stored instant is returned rather than recomputed by the caller. Two independent clocks
	// would let the JSON the author sees disagree with the row the server enforces, and the editor's
	// countdown should count down to the instant that is actually stored.
	return token, expiresAt, nil
}

// ResolvePrivateLink validates a token and returns the session it grants.
//
// The lookup is by the token's hash, so the raw token is never placed in a query. A deleted work's
// link is treated as absent even though its row survives: this application soft-deletes by setting
// deleted_at, so the ON DELETE CASCADE in the migration never fires. The join is what actually
// withdraws the capability, and it must be repeated in every path that uses a link.
func ResolvePrivateLink(ctx context.Context, token string) (PrivateLinkSession, error) {
	var session PrivateLinkSession

	if err := database.Require(); err != nil {
		return session, err
	}

	if token == "" {
		return session, ErrNoLink
	}

	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])

	var linkID, poemID string
	var expiresAt time.Time
	var revokedAt *time.Time

	row := database.Pool.QueryRow(ctx, `
        SELECT l.id, l.poem_id, l.expires_at, l.revoked_at
        FROM private_links l
        JOIN poems p ON p.id = l.poem_id AND p.deleted_at IS NULL
        WHERE l.token_hash = $1`, hash)
	err := row.Scan(&linkID, &poemID, &expiresAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, ErrNoLink
	}
	if err != nil {
		return session, err
	}

	if revokedAt != nil {
		return session, ErrLinkRevoked
	}
	if clock.NowUTC().After(expiresAt) {
		return session, ErrLinkExpired
	}

	return PrivateLinkSession{LinkID: linkID, PoemID: poemID, ExpiresAt: expiresAt}, nil
}

// PrivateLinkActive reports whether a link may still be used, and returns its work and expiry.
//
// The guest middleware calls this on every request, not just at login. A link revoked after a guest
// session was issued must stop working immediately; the signed cookie alone cannot know that. The
// expiry is returned as well so the editor can show the remaining time from the same instant the
// server enforces.
func PrivateLinkActive(ctx context.Context, linkID string) (poemID string, expiresAt time.Time, err error) {
	if err := database.Require(); err != nil {
		return "", time.Time{}, err
	}

	err = database.Pool.QueryRow(ctx, `
        SELECT l.poem_id, l.expires_at
        FROM private_links l
        JOIN poems p ON p.id = l.poem_id AND p.deleted_at IS NULL
        WHERE l.id = $1 AND l.revoked_at IS NULL AND l.expires_at > $2`, linkID, clock.NowUTC()).
		Scan(&poemID, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, ErrNoLink
	}
	if err != nil {
		return "", time.Time{}, err
	}
	return poemID, expiresAt, nil
}

// ListPrivateLinks returns a work's links, newest first, including expired and revoked ones.
//
// The Share screen lists them so the author can see what has been handed out and revoke any link
// before it expires. Hiding expired links would hide the fact that a capability existed.
func ListPrivateLinks(ctx context.Context, poemID string) ([]PrivateLink, error) {
	if err := database.Require(); err != nil {
		return nil, err
	}

	rows, err := database.Pool.Query(ctx, `
        SELECT id, poem_id, created_at, expires_at, revoked_at
        FROM private_links
        WHERE poem_id = $1
        ORDER BY created_at DESC`, poemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PrivateLink
	for rows.Next() {
		var link PrivateLink
		if err := rows.Scan(&link.ID, &link.PoemID, &link.CreatedAt, &link.ExpiresAt, &link.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

// RevokePrivateLink invalidates a link and every proposal path through it.
//
// Scoped by poem_id as well as the link id: a request that names a valid link belonging to another
// work must not be able to revoke it. The returned error distinguishes a miss from a wrong-work
// request so the handler can answer 404 without revealing that the other link exists.
//
// The instant comes from the application clock rather than SQL now() so the same seam controls every
// expiry decision, including the ones in the guest middleware.
func RevokePrivateLink(ctx context.Context, poemID, linkID string) error {
	if err := database.Require(); err != nil {
		return err
	}

	tag, err := database.Pool.Exec(ctx, `
        UPDATE private_links SET revoked_at = $3
        WHERE id = $1 AND poem_id = $2 AND revoked_at IS NULL`, linkID, poemID, clock.NowUTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNoLink
	}
	return nil
}

// ListWorkProposals returns a work's proposals, newest first.
func ListWorkProposals(ctx context.Context, poemID string) ([]WorkProposal, error) {
	if err := database.Require(); err != nil {
		return nil, err
	}

	// Primary first within a link, then by last update. An updated submission must not stay pinned
	// at its original created_at position in History.
	rows, err := database.Pool.Query(ctx, `
        SELECT id, poem_id, link_id, branch, guest_name, content, created_at, updated_at, applied_at
        FROM work_proposals
        WHERE poem_id = $1
        ORDER BY updated_at DESC, created_at DESC, id DESC`, poemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorkProposal
	for rows.Next() {
		var proposal WorkProposal
		if err := rows.Scan(&proposal.ID, &proposal.PoemID, &proposal.LinkID, &proposal.Branch,
			&proposal.GuestName, &proposal.Content, &proposal.CreatedAt, &proposal.UpdatedAt,
			&proposal.AppliedAt); err != nil {
			return nil, err
		}
		out = append(out, proposal)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

// CountProposalsByLink returns a work's suggestion counts keyed by the link that carried them.
//
// The Share screen needs only counts. Loading every proposal to derive them would pull full bodies
// -- up to 200,000 runes each -- into a page whose purpose is choosing a work, and the cost would
// grow with the number of suggestions a work has received.
func CountProposalsByLink(ctx context.Context, poemID string) (map[string]int, error) {
	if err := database.Require(); err != nil {
		return nil, err
	}

	rows, err := database.Pool.Query(ctx, `
        SELECT link_id, count(*)::bigint
        FROM work_proposals
        WHERE poem_id = $1
        GROUP BY link_id`, poemID)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}

// ApplyWorkProposal writes a suggestion to the work and marks it applied, atomically.
//
// This used to be two statements -- UpdatePoem followed by a separate mark -- which let two
// simultaneous applies both pass the "not yet applied" check, write the draft twice, and insert two
// history versions. It also left the content changed but the proposal unmarked if the process died
// between them, so a retry would duplicate the version again.
//
// The proposal row is locked first, then the poem, in one transaction. The submit path locks the
// draft before the proposal, so neither path holds the other's lock when it asks for its own. The
// retained-version rule is the same one UpdatePoem uses: a version is recorded only when the text
// actually changes. Applying an identical suggestion is a no-op for history, but still marks the
// proposal as dealt with.
//
// Applying one branch unmarks its siblings, so exactly one submission per work is "applied" at a
// time. Submitting new text on the primary branch also clears its own applied_at in the upsert.
func ApplyWorkProposal(ctx context.Context, poemID, proposalID string) error {
	if err := database.Require(); err != nil {
		return err
	}

	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { discardRollback(tx.Rollback(ctx)) }()

	var content string
	err = tx.QueryRow(ctx, `
        SELECT content FROM work_proposals
        WHERE id = $1 AND poem_id = $2 AND applied_at IS NULL
        FOR UPDATE`, proposalID, poemID).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoProposal
	}
	if err != nil {
		return err
	}

	// Same deleted_at guard as UpdatePoem: a work in the recycle cannot be edited through a stale
	// proposal, and its link was withdrawn when it was deleted.
	var previous string
	err = tx.QueryRow(ctx, `
        SELECT content FROM poems WHERE id = $1 AND deleted_at IS NULL
        FOR UPDATE`, poemID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if previous != content {
		if _, err := tx.Exec(ctx,
			`INSERT INTO poem_versions (id, poem_id, content) VALUES ($1, $2, $3)`,
			uuid.NewString(), poemID, previous); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE poems SET content = $1 WHERE id = $2`, content, poemID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE work_proposals SET applied_at = NULL WHERE poem_id = $1 AND id <> $2 AND applied_at IS NOT NULL`,
		poemID, proposalID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE work_proposals SET applied_at = $2 WHERE id = $1`, proposalID, clock.NowUTC()); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
