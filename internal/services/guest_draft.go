package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/database"
)

// Errors returned by the guest draft and submission paths.
var (
	// ErrNoChanges means the body still matches the last thing the guest sent.
	ErrNoChanges = errors.New("nothing has changed since the last submission")
	// ErrEmptyContent is the draft-layer equivalent of the handler's validation error.
	ErrEmptyContent = errors.New("a work needs some text")
	// ErrContentTooLong bounds a draft to the same width as a work body.
	ErrContentTooLong = errors.New("that work is longer than this build accepts")
	// ErrNoDraft means the guest editor has not been opened for this link.
	ErrNoDraft = errors.New("no draft for this link")
	// ErrTooManyAlternatives bounds the branches one link can create.
	ErrTooManyAlternatives = errors.New("this link has sent as many alternative versions as it can")
	// ErrInvalidSubmission rejects a client-supplied id that is not a UUID.
	ErrInvalidSubmission = errors.New("invalid submission id")
)

// MaxAlternativesPerLink bounds storage and keeps the author's History readable.
const MaxAlternativesPerLink = 20

// PrimaryBranch is the single, updated submission a link has by default.
const PrimaryBranch = "primary"

// GuestDraft is one continuously edited body, stored once per private link.
type GuestDraft struct {
	LinkID    string
	PoemID    string
	GuestName string
	Content   string
	BaseHash  string
	UpdatedAt time.Time
}

// normalizeDraftContent makes the body canonical in three ways: stable line endings, trimmed edges,
// and a bounded length. Trimming here matters twice. A whitespace-only edit must not pass the change
// guard and then be stored as the same text the author already has; and a leading newline written
// into a <textarea> is silently dropped by the HTML parser, so leaving it in the stored body would
// make every reload report a change the guest did not make.
func normalizeDraftContent(raw string) (string, error) {
	normalised := strings.ReplaceAll(raw, "\r\n", "\n")
	normalised = strings.ReplaceAll(normalised, "\r", "\n")
	normalised = strings.TrimSpace(normalised)
	if len([]rune(normalised)) > maxDraftRunes {
		return "", ErrContentTooLong
	}
	return normalised, nil
}

// maxDraftRunes mirrors the work body's bound; the two must stay in step.
const maxDraftRunes = 200_000

// ContentHash is SHA-256 of a normalized body, exposed so the handler can render the initial
// changed state from the same rule the service uses to accept a submission.
func ContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

const draftColumns = `link_id, poem_id, guest_name, content, base_hash, updated_at`

func scanDraft(row pgx.Row) (GuestDraft, error) {
	var draft GuestDraft
	err := row.Scan(&draft.LinkID, &draft.PoemID, &draft.GuestName, &draft.Content,
		&draft.BaseHash, &draft.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return draft, ErrNoDraft
	}
	return draft, err
}

// GetOrCreateGuestDraft returns the link's body, seeding it from the work on first open.
//
// The baseline is the body the guest was given, so the first edit is detectable. An existing draft
// is never overwritten here -- reloading the editor is not a new conversation.
func GetOrCreateGuestDraft(ctx context.Context, session PrivateLinkSession, originalContent string) (GuestDraft, error) {
	if err := requireActiveLink(ctx, session); err != nil {
		return GuestDraft{}, err
	}
	normalised, err := normalizeDraftContent(originalContent)
	if err != nil {
		return GuestDraft{}, err
	}

	if _, err := database.Pool.Exec(ctx, `
        INSERT INTO guest_drafts (`+draftColumns+`)
        VALUES ($1, $2, '', $3, $4, $5)
        ON CONFLICT (link_id) DO NOTHING`,
		session.LinkID, session.PoemID, normalised, ContentHash(normalised), clock.NowUTC()); err != nil {
		return GuestDraft{}, err
	}
	return scanDraft(database.Pool.QueryRow(ctx,
		`SELECT `+draftColumns+` FROM guest_drafts WHERE link_id = $1`, session.LinkID))
}

// SaveGuestDraft upserts the continuous body and reports whether it differs from the last send.
func SaveGuestDraft(ctx context.Context, session PrivateLinkSession, rawContent, name string) (GuestDraft, bool, error) {
	if err := requireActiveLink(ctx, session); err != nil {
		return GuestDraft{}, false, err
	}
	normalised, err := normalizeDraftContent(rawContent)
	if err != nil {
		return GuestDraft{}, false, err
	}

	if _, err := database.Pool.Exec(ctx, `
        INSERT INTO guest_drafts (`+draftColumns+`)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (link_id) DO UPDATE SET
            guest_name = EXCLUDED.guest_name,
            content = EXCLUDED.content,
            updated_at = EXCLUDED.updated_at`,
		session.LinkID, session.PoemID, cleanGuestNameForService(name), normalised,
		ContentHash(normalised), clock.NowUTC()); err != nil {
		return GuestDraft{}, false, err
	}
	draft, err := scanDraft(database.Pool.QueryRow(ctx,
		`SELECT `+draftColumns+` FROM guest_drafts WHERE link_id = $1`, session.LinkID))
	return draft, draft.BaseHash != ContentHash(normalised), err
}

// SubmitGuestDraft sends the current body. A primary submission is upserted; an alternative gets a
// unique branch id and is never updated again. Both advance the draft's baseline in the same
// transaction, so a second click cannot send the same text twice.
func SubmitGuestDraft(ctx context.Context, session PrivateLinkSession, rawContent, name string, alternative bool, submissionID string) (proposalID, branch string, err error) {
	if err := requireActiveLink(ctx, session); err != nil {
		return "", "", err
	}
	normalised, err := normalizeDraftContent(rawContent)
	if err != nil {
		return "", "", err
	}
	submission := strings.TrimSpace(normalised)
	if submission == "" {
		return "", "", ErrEmptyContent
	}
	hash := ContentHash(normalised)

	tx, err := database.Pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer func() { discardRollback(tx.Rollback(ctx)) }()

	// Lock the draft before the proposal. ApplyWorkProposal locks proposal then poem, so this order
	// keeps the two paths acyclic.
	var baseHash string
	err = tx.QueryRow(ctx,
		`SELECT base_hash FROM guest_drafts WHERE link_id = $1 FOR UPDATE`, session.LinkID).
		Scan(&baseHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNoDraft
	}
	if err != nil {
		return "", "", err
	}
	if baseHash == hash {
		return "", "", ErrNoChanges
	}

	branch = PrimaryBranch
	if alternative {
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT count(*)::int FROM work_proposals WHERE link_id = $1 AND branch <> $2`,
			session.LinkID, PrimaryBranch).Scan(&count); err != nil {
			return "", "", err
		}
		if count >= MaxAlternativesPerLink {
			return "", "", ErrTooManyAlternatives
		}
		id := submissionID
		if id == "" {
			id = uuid.NewString()
		} else {
			parsed, err := uuid.Parse(id)
			if err != nil || parsed.String() != id {
				return "", "", ErrInvalidSubmission
			}
		}
		branch = "alt:" + id
	}
	id := uuid.NewString()
	now := clock.NowUTC()

	err = tx.QueryRow(ctx, `
        INSERT INTO work_proposals (id, poem_id, link_id, branch, guest_name, content, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
        ON CONFLICT (link_id, branch) DO UPDATE SET
            guest_name = EXCLUDED.guest_name,
            content = EXCLUDED.content,
            updated_at = EXCLUDED.updated_at,
            applied_at = CASE
                WHEN work_proposals.content IS DISTINCT FROM EXCLUDED.content THEN NULL
                ELSE work_proposals.applied_at END
        RETURNING id`, id, session.PoemID, session.LinkID, branch,
		cleanGuestNameForService(name), submission, now).Scan(&proposalID)
	if err != nil {
		return "", "", err
	}

	if _, err := tx.Exec(ctx, `
        UPDATE guest_drafts
        SET content = $2, guest_name = $3, base_hash = $4, updated_at = $5
        WHERE link_id = $1`, session.LinkID, normalised, cleanGuestNameForService(name), hash, now); err != nil {
		return "", "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return proposalID, branch, nil
}

// cleanGuestNameForService mirrors the handler's bound without importing the handler package.
func cleanGuestNameForService(raw string) string {
	name := strings.Join(strings.Fields(raw), " ")
	if name == "" {
		return "Anonymous"
	}
	if len([]rune(name)) > 80 {
		name = string([]rune(name)[:80])
	}
	return name
}

// requireActiveLink repeats the link check inside the service so a caller can never bypass the
// middleware with a crafted session value.
func requireActiveLink(ctx context.Context, session PrivateLinkSession) error {
	poemID, _, err := PrivateLinkActive(ctx, session.LinkID)
	if err != nil {
		return err
	}
	if poemID != session.PoemID {
		return ErrNoLink
	}
	return nil
}
