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

	// TokenSeed is NULL for links minted before v0.5.9; their tokens cannot be recovered. It is a
	// sql.NullString because pgx refuses to scan a NULL into a plain string, which would otherwise
	// make one legacy row hide every link on the work when Share swallows the list error.
	TokenSeed sql.NullString
	TokenHash string
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

// GetOrCreatePrivateLink returns the work's newest recoverable active link, or mints one when there
// is none. It never extends an existing link's expiry and never changes its token: a repeated click
// is a read, not a second capability. The poem row is locked for the whole decision so concurrent
// clicks cannot both report that they created a link, and a revoke taking the same lock cannot turn
// the returned URL into a dead one between the read and the commit.
func GetOrCreatePrivateLink(ctx context.Context, key linktoken.Key, poemID string) (token, id, expiresAt string, created bool, err error) {
	return createOrReusePrivateLink(ctx, key, poemID, false)
}

// CreatePrivateLink always mints a fresh row. It backs the explicit "New private link" action: each
// row is one reviewer's conversation, so a new one is added rather than replacing an existing link
// and invalidating the guest who holds it.
func CreatePrivateLink(ctx context.Context, key linktoken.Key, poemID string) (token, id, expiresAt string, err error) {
	token, id, expiresAt, _, err = createOrReusePrivateLink(ctx, key, poemID, true)
	return token, id, expiresAt, err
}

func createOrReusePrivateLink(ctx context.Context, key linktoken.Key, poemID string, forceNew bool) (token, id, expiresAt string, created bool, err error) {
	if err := database.Require(); err != nil {
		return "", "", "", false, err
	}

	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		return "", "", "", false, err
	}
	defer func() { discardRollback(tx.Rollback(ctx)) }()

	// The poem row is the mutex for every mint on this work. ApplyWorkProposal locks proposal then
	// poem, so taking only the poem lock here cannot close a cycle. The deleted_at guard withdraws
	// links to works in the recycle.
	var probe int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM poems WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, poemID).Scan(&probe); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", "", false, ErrNotFound
		}
		return "", "", "", false, err
	}

	now := clock.NowUTC()
	if !forceNew {
		// Newest first, with an id tiebreak: clock.NowUTC() is a seam, so tests can make two mints
		// share an instant and the choice must still be deterministic.
		rows, err := tx.Query(ctx, `
            SELECT id, token_seed, token_hash, expires_at
            FROM private_links
            WHERE poem_id = $1 AND revoked_at IS NULL AND expires_at > $2
            ORDER BY created_at DESC, id DESC`, poemID, now)
		if err != nil {
			return "", "", "", false, err
		}
		found := false
		for rows.Next() {
			var rowID, hash string
			var seed sql.NullString
			var expiry time.Time
			if err := rows.Scan(&rowID, &seed, &hash, &expiry); err != nil {
				rows.Close()
				return "", "", "", false, err
			}
			if !seed.Valid {
				continue // a pre-v0.5.9 row cannot be recovered; an older recoverable row may still serve
			}
			candidate, err := linktoken.Recover(key, linktoken.Private, seed.String, hash)
			if err != nil {
				continue // a rotated key cannot reproduce this row's token
			}
			token, id, expiresAt = candidate, rowID, expiry.UTC().Format(time.RFC3339)
			found = true
			break
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return "", "", "", false, rowErr
		}
		if found {
			if err := tx.Commit(ctx); err != nil {
				return "", "", "", false, err
			}
			return token, id, expiresAt, false, nil
		}
	}

	seed, token, err := linktoken.Mint(key, linktoken.Private)
	if err != nil {
		return "", "", "", false, err
	}
	hash := linktoken.HashToken(token)
	expiry := now.Add(PrivateLinkTTL)
	linkID, err := uuid.NewRandom()
	if err != nil {
		return "", "", "", false, fmt.Errorf("generate a private link id: %w", err)
	}
	tag, err := tx.Exec(ctx, `
        INSERT INTO private_links (id, poem_id, token_hash, token_seed, created_at, expires_at)
        VALUES ($1, $2, $3, $4, $5, $6)`,
		linkID, poemID, hash, seed, now, expiry)
	if err != nil {
		return "", "", "", false, err
	}
	if tag.RowsAffected() == 0 {
		return "", "", "", false, ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", "", false, err
	}

	// Return the instant that was written so the editor's countdown matches the row the server
	// enforces. UTC keeps this string identical to the one the reuse path formats from the row.
	return token, linkID.String(), expiry.UTC().Format(time.RFC3339), true, nil
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

	hash := linktoken.HashToken(token)

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
	if !clock.NowUTC().Before(expiresAt) {
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
// The Share screen filters expired rows out before rendering: the row remains for history, but a
// capability that can no longer be used has nothing left for the author to act on.
func ListPrivateLinks(ctx context.Context, poemID string) ([]PrivateLink, error) {
	if err := database.Require(); err != nil {
		return nil, err
	}

	rows, err := database.Pool.Query(ctx, `
        SELECT id, poem_id, created_at, expires_at, revoked_at, token_seed, token_hash
        FROM private_links
        WHERE poem_id = $1
        ORDER BY created_at DESC, id DESC`, poemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PrivateLink
	for rows.Next() {
		var link PrivateLink
		if err := rows.Scan(&link.ID, &link.PoemID, &link.CreatedAt, &link.ExpiresAt, &link.RevokedAt, &link.TokenSeed, &link.TokenHash); err != nil {
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

	// Take the same poem lock as mint, in the same order. Without it a revoke can commit between
	// mint's read of an active row and mint's response, handing the author a URL that is already
	// dead. The lock is held until the revoke commits, so the two actions linearize cleanly.
	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { discardRollback(tx.Rollback(ctx)) }()

	var probe int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM poems WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, poemID).Scan(&probe); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoLink
		}
		return err
	}

	tag, err := tx.Exec(ctx, `
        UPDATE private_links SET revoked_at = $3
        WHERE id = $1 AND poem_id = $2 AND revoked_at IS NULL`, linkID, poemID, clock.NowUTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNoLink
	}
	return tx.Commit(ctx)
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

// ApplyWorkProposal writes a suggestion and marks it applied in one transaction, so a double click
// cannot write the draft twice. The proposal is locked before the poem; the submit path locks a
// draft before a proposal, so the two paths cannot deadlock. A version is retained only when the text
// changes, as in UpdatePoem. Applying one branch unmarks its siblings, leaving one applied submission
// per work.
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
