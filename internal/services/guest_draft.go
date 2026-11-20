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
	// ErrNoChanges means the body still matches the last thing the guest sent on this branch.
	ErrNoChanges = errors.New("nothing has changed since the last submission")
	// ErrEmptyContent is the draft-layer equivalent of the handler's validation error.
	ErrEmptyContent = errors.New("a work needs some text")
	// ErrContentTooLong bounds a draft to the same width as a work body.
	ErrContentTooLong = errors.New("that work is longer than this build accepts")
	// ErrNoDraft means the requested branch has no draft for this link.
	ErrNoDraft = errors.New("no draft for this link")
	// ErrTooManyAlternatives bounds the branches one link can create.
	ErrTooManyAlternatives = errors.New("this link has sent as many alternative versions as it can")
	// ErrInvalidSubmission rejects a client-supplied id or branch that is not a canonical UUID.
	ErrInvalidSubmission = errors.New("invalid submission id")
)

// MaxAlternativesPerLink bounds storage and keeps the author's History readable.
const MaxAlternativesPerLink = 20

// PrimaryBranch is the branch seeded from the work when the link is first opened.
const PrimaryBranch = "primary"

// NewBranch tells SubmitGuestDraft to create a fresh alternative branch.
const NewBranch = "new"

// GuestDraft is one continuously edited body, keyed by link and branch.
type GuestDraft struct {
	LinkID    string
	PoemID    string
	Branch    string
	GuestName string
	Content   string
	BaseHash  string
	UpdatedAt time.Time
}

// GuestBranch is one tab in the guest editor's branch rail.
type GuestBranch struct {
	Branch    string
	Content   string
	Changed   bool
	Submitted bool
	UpdatedAt time.Time
}

// normalizeDraftContent makes the body canonical: stable line endings, trimmed edges and a bounded
// length. Trimming matters twice -- a whitespace-only edit must not pass the change guard, and a
// leading newline is silently dropped by an HTML <textarea>, so storing one would make every reload
// report a change the guest did not make.
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

// ContentHash is SHA-256 of a canonical body, exposed so handlers can render the initial changed
// state from the same rule the service uses to accept a submission.
func ContentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

const draftColumns = `link_id, poem_id, branch, guest_name, content, base_hash, updated_at`

func scanDraft(row pgx.Row) (GuestDraft, error) {
	var draft GuestDraft
	err := row.Scan(&draft.LinkID, &draft.PoemID, &draft.Branch, &draft.GuestName, &draft.Content,
		&draft.BaseHash, &draft.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return draft, ErrNoDraft
	}
	return draft, err
}

// ValidateBranch checks that a branch name is either the primary branch or an alternative id the
// service itself would have generated. It is the single guard against crafted branch values.
func ValidateBranch(branch string) error {
	if branch == PrimaryBranch {
		return nil
	}
	if !strings.HasPrefix(branch, "alt:") {
		return ErrInvalidSubmission
	}
	id := strings.TrimPrefix(branch, "alt:")
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return ErrInvalidSubmission
	}
	return nil
}

// GetOrCreateGuestDraft returns the branch's body, seeding it from seedContent on first open. The
// baseline is the body the guest was given, so the first edit is detectable. Existing drafts are
// never overwritten here: a reload is not a new conversation.
func GetOrCreateGuestDraft(ctx context.Context, session PrivateLinkSession, branch, seedContent string) (GuestDraft, error) {
	if err := requireActiveLink(ctx, session); err != nil {
		return GuestDraft{}, err
	}
	if err := ValidateBranch(branch); err != nil {
		return GuestDraft{}, err
	}
	normalised, err := normalizeDraftContent(seedContent)
	if err != nil {
		return GuestDraft{}, err
	}

	if _, err := database.Pool.Exec(ctx, `
        INSERT INTO guest_drafts (`+draftColumns+`)
        VALUES ($1, $2, $3, '', $4, $5, $6)
        ON CONFLICT (link_id, branch) DO NOTHING`,
		session.LinkID, session.PoemID, branch, normalised, ContentHash(normalised), clock.NowUTC()); err != nil {
		return GuestDraft{}, err
	}
	return scanDraft(database.Pool.QueryRow(ctx,
		`SELECT `+draftColumns+` FROM guest_drafts WHERE link_id = $1 AND branch = $2`,
		session.LinkID, branch))
}

// GetGuestDraft returns an existing branch without creating one. Only the primary branch may be
// seeded implicitly by opening the editor; alternatives come into being through an explicit send.
func GetGuestDraft(ctx context.Context, session PrivateLinkSession, branch string) (GuestDraft, error) {
	if err := requireActiveLink(ctx, session); err != nil {
		return GuestDraft{}, err
	}
	if err := ValidateBranch(branch); err != nil {
		return GuestDraft{}, err
	}
	return scanDraft(database.Pool.QueryRow(ctx,
		`SELECT `+draftColumns+` FROM guest_drafts WHERE link_id = $1 AND branch = $2`,
		session.LinkID, branch))
}

// SaveGuestDraft upserts one branch's continuous body and reports whether it differs from the last
// send on that branch.
func SaveGuestDraft(ctx context.Context, session PrivateLinkSession, branch, rawContent, name string) (GuestDraft, bool, error) {
	if err := requireActiveLink(ctx, session); err != nil {
		return GuestDraft{}, false, err
	}
	if err := ValidateBranch(branch); err != nil {
		return GuestDraft{}, false, err
	}
	normalised, err := normalizeDraftContent(rawContent)
	if err != nil {
		return GuestDraft{}, false, err
	}

	if _, err := database.Pool.Exec(ctx, `
        INSERT INTO guest_drafts (`+draftColumns+`)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        ON CONFLICT (link_id, branch) DO UPDATE SET
            guest_name = EXCLUDED.guest_name,
            content = EXCLUDED.content,
            updated_at = EXCLUDED.updated_at`,
		session.LinkID, session.PoemID, branch, cleanGuestNameForService(name), normalised,
		ContentHash(normalised), clock.NowUTC()); err != nil {
		return GuestDraft{}, false, err
	}
	draft, err := scanDraft(database.Pool.QueryRow(ctx,
		`SELECT `+draftColumns+` FROM guest_drafts WHERE link_id = $1 AND branch = $2`,
		session.LinkID, branch))
	if err != nil {
		return GuestDraft{}, false, err
	}
	return draft, draft.BaseHash != ContentHash(draft.Content), nil
}

// ListGuestBranches returns the editor's tabs, scoped to one link. A draft may exist without a
// submission (the guest opened a new alternative but has not sent it yet), and a submission may be
// older than the current draft; both states are reported so the tab can show them honestly.
func ListGuestBranches(ctx context.Context, linkID string) ([]GuestBranch, error) {
	rows, err := database.Pool.Query(ctx, `
        SELECT d.branch, d.content, d.base_hash, d.updated_at,
               EXISTS (SELECT 1 FROM work_proposals p
                       WHERE p.link_id = d.link_id AND p.branch = d.branch)
        FROM guest_drafts d
        WHERE d.link_id = $1
        ORDER BY (d.branch = $2) DESC, d.updated_at DESC, d.branch DESC`, linkID, PrimaryBranch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []GuestBranch
	for rows.Next() {
		var branch GuestBranch
		var baseHash string
		if err := rows.Scan(&branch.Branch, &branch.Content, &baseHash, &branch.UpdatedAt,
			&branch.Submitted); err != nil {
			return nil, err
		}
		branch.Changed = baseHash != ContentHash(branch.Content)
		out = append(out, branch)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SubmitGuestDraft sends the current body of one branch. target is "primary", "new" for a fresh
// alternative, or an existing "alt:<uuid>" branch. Every case advances only that branch's baseline
// in the same transaction, so a second click cannot send the same text twice.
func SubmitGuestDraft(ctx context.Context, session PrivateLinkSession, target, sourceBranch, rawContent, name, submissionID string) (proposalID, branch string, err error) {
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

	branch = target
	if target == NewBranch {
		branch, err = createAlternativeBranch(ctx, tx, session, sourceBranch, submissionID)
		if err != nil {
			return "", "", err
		}
	} else if err := ValidateBranch(target); err != nil {
		return "", "", err
	}

	// Lock the branch's draft before the proposal. ApplyWorkProposal locks proposal then poem, so
	// neither path holds the other's lock when it asks for its own.
	var baseHash string
	err = tx.QueryRow(ctx,
		`SELECT base_hash FROM guest_drafts
         WHERE link_id = $1 AND branch = $2 FOR UPDATE`, session.LinkID, branch).
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
        WHERE link_id = $1 AND branch = $6`,
		session.LinkID, normalised, cleanGuestNameForService(name), hash, now, branch); err != nil {
		return "", "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return proposalID, branch, nil
}

// createAlternativeBranch seeds a new branch from the draft the guest is currently viewing, so an
// alternative starts as a copy of the text in front of them and the first edit is detectable.
func createAlternativeBranch(ctx context.Context, tx pgx.Tx, session PrivateLinkSession, sourceBranch, submissionID string) (string, error) {
	if sourceBranch == "" || sourceBranch == NewBranch {
		sourceBranch = PrimaryBranch
	}
	if err := ValidateBranch(sourceBranch); err != nil {
		return "", err
	}
	var count int
	if err := tx.QueryRow(ctx,
		`SELECT count(*)::int FROM guest_drafts WHERE link_id = $1 AND branch <> $2`,
		session.LinkID, PrimaryBranch).Scan(&count); err != nil {
		return "", err
	}
	if count >= MaxAlternativesPerLink {
		return "", ErrTooManyAlternatives
	}

	id := submissionID
	if id == "" {
		id = uuid.NewString()
	} else {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return "", ErrInvalidSubmission
		}
	}
	branch := "alt:" + id

	var content, name string
	if err := tx.QueryRow(ctx,
		`SELECT content, guest_name FROM guest_drafts WHERE link_id = $1 AND branch = $2`,
		session.LinkID, sourceBranch).Scan(&content, &name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNoDraft
		}
		return "", err
	}
	// Seed from the last submitted text, not the current autosave. The guest types an alternative in
	// the existing editor, so the current draft already holds the new text; comparing against it
	// would make the branch appear unchanged and refuse the very send that created it.
	var submitted string
	submitErr := tx.QueryRow(ctx,
		`SELECT content FROM work_proposals WHERE link_id = $1 AND branch = $2`,
		session.LinkID, sourceBranch).Scan(&submitted)
	if submitErr == nil {
		content = submitted
	} else if !errors.Is(submitErr, pgx.ErrNoRows) {
		return "", submitErr
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO guest_drafts (`+draftColumns+`)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        ON CONFLICT (link_id, branch) DO UPDATE SET updated_at = EXCLUDED.updated_at`,
		session.LinkID, session.PoemID, branch, name, content, ContentHash(content), clock.NowUTC()); err != nil {
		return "", err
	}
	return branch, nil
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
