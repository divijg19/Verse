package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	page "github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/presenters"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// This file holds the two surfaces a private link creates: the entry route that exchanges
// a link token for a scoped guest session, and the guest's own editor and proposal endpoints.
//
// The author's handler for the same link -- creating, listing and revoking it -- lives in share.go.

// maxGuestName bounds the optional name a guest may attach to a proposal.
//
// It is not a place for markup: the name is rendered with HTML escaping by templ, but a bound also
// stops a very long string from crowding the history list and from being used as a storage abuse
// vector by someone who holds a link.
const maxGuestName = 80

// guestEditorPath is where an entry redirects and where the limited editor lives.
const guestEditorPath = "/guest/edit"

// PrivateLinkEntryHandler is the public entry for a private link.
//
// A token is exchanged for a short-lived, work-scoped session cookie and the browser is redirected
// to the guest editor, which drops the raw token from the address bar. The raw token is never put in
// a cookie or a log; only its hash is stored, and only the signed session id reaches the browser.
//
// Every dead-link failure is a bare 404. Whether a link expired, was revoked, or never existed is not
// information the holder of a dead URL is entitled to, and the three cases must look identical.
func PrivateLinkEntryHandler(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	if token == "" {
		http.NotFound(w, r)
		return
	}

	session, err := services.ResolvePrivateLink(r.Context(), token)
	if err != nil {
		if !errors.Is(err, services.ErrNoLink) &&
			!errors.Is(err, services.ErrLinkExpired) &&
			!errors.Is(err, services.ErrLinkRevoked) {
			fail500(w, r, "failed to resolve a private link", err)
			return
		}
		http.NotFound(w, r)
		return
	}

	// The session builder lives in the HTTP layer because that is where the signing secret is.
	// This handler cannot call it directly without importing internal/server, which would be a
	// cycle; the exchange is completed by a helper the router injects into the request context.
	issue, ok := guestSessionIssuerFrom(r.Context())
	if !ok {
		fail500(w, r, "guest session issuer is not configured", errors.New("missing guest session issuer"))
		return
	}
	if err := issue(w, r, session); err != nil {
		fail500(w, r, "failed to start a guest session", err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, guestEditorPath, http.StatusSeeOther)
}

// GuestEditorHandler renders the limited editor for the work named by the guest session.
//
// The work id comes from the context, never from the URL or the form. A guest has no way to name a
// different work, and a crafted request is simply the one work they were given.
func GuestEditorHandler(w http.ResponseWriter, r *http.Request) {
	guest, ok := GuestSessionFrom(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}

	poem, err := services.GetPoem(r.Context(), guest.PoemID)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		fail500(w, r, "failed to load the shared work", err)
		return
	}

	session := services.PrivateLinkSession{LinkID: guest.LinkID, PoemID: guest.PoemID}

	// The primary draft is seeded from the work on first open. Alternatives are real branches with
	// their own continuous body, created by an explicit send; a crafted branch cannot create one.
	primary, err := services.GetOrCreateGuestDraft(r.Context(), session, services.PrimaryBranch, poem.Content)
	if err != nil {
		writeGuestLoadError(w, r, err)
		return
	}

	branch := r.URL.Query().Get("branch")
	if branch == "" {
		branch = services.PrimaryBranch
	}
	draft := primary
	if branch != services.PrimaryBranch {
		draft, err = services.GetGuestDraft(r.Context(), session, branch)
		if err != nil {
			writeGuestLoadError(w, r, err)
			return
		}
	}

	branches, err := services.ListGuestBranches(r.Context(), guest.LinkID)
	if err != nil {
		fail500(w, r, "failed to list the shared branches", err)
		return
	}

	remaining := guest.ExpiresAt.Sub(clock.NowUTC())
	if remaining < 0 {
		remaining = 0
	}

	branchViews := make([]templ.GuestBranchView, 0, len(branches))
	alternative := 0
	for _, item := range branches {
		label := "Main version"
		if item.Branch != services.PrimaryBranch {
			alternative++
			label = fmt.Sprintf("Alternative %d", alternative)
		}
		branchViews = append(branchViews, templ.GuestBranchView{
			Branch:    item.Branch,
			Label:     label,
			Selected:  item.Branch == branch,
			Changed:   item.Changed,
			Submitted: item.Submitted,
		})
	}

	page := templ.GuestEditor(templ.GuestEditorData{
		Title:     presenters.WorkTitle(poem.Content, presenters.TitleWidthWide),
		Content:   draft.Content,
		GuestName: draft.GuestName,
		Changed:   draft.BaseHash != services.ContentHash(draft.Content),
		Branch:    draft.Branch,
		Branches:  branchViews,
		ExpiresAt: guest.ExpiresAt,
		Minutes:   int(remaining / time.Minute),
	})
	renderGuest(w, r, page)
}

func writeGuestLoadError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, services.ErrNoLink), errors.Is(err, services.ErrNoDraft),
		errors.Is(err, services.ErrNotFound), errors.Is(err, services.ErrInvalidSubmission):
		http.NotFound(w, r)
	default:
		fail500(w, r, "failed to load the shared draft", err)
	}
}

// GuestDraftHandler autosaves the continuous body for this link.
//
// It answers JSON, not an HTML fragment: the guest script parses status and change state from the
// response, and an HTML validation page would make response.json() throw. CSRF and link liveness are
// enforced by the middleware and again by the service.
func GuestDraftHandler(w http.ResponseWriter, r *http.Request) {
	guest, ok := GuestSessionFrom(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}

	draft, changed, err := services.SaveGuestDraft(r.Context(), services.PrivateLinkSession{
		LinkID: guest.LinkID, PoemID: guest.PoemID,
	}, branchFromForm(r), r.PostFormValue("content"), r.PostFormValue("name"))
	if err != nil {
		writeGuestJSONError(w, err)
		return
	}
	writeGuestJSON(w, struct {
		SavedAt time.Time `json:"savedAt"`
		Changed bool      `json:"changed"`
		Name    string    `json:"name"`
	}{SavedAt: draft.UpdatedAt, Changed: changed, Name: draft.GuestName})
}

// GuestProposalHandler submits the current body.
//
// A primary submission replaces the link's single primary row; an alternative creates a new branch.
// Both advance the draft baseline in one transaction, so sending the same text twice is refused.
// The author's work is never touched here.
func GuestProposalHandler(w http.ResponseWriter, r *http.Request) {
	guest, ok := GuestSessionFrom(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}

	_, _, err := services.SubmitGuestDraft(r.Context(), services.PrivateLinkSession{
		LinkID: guest.LinkID,
		PoemID: guest.PoemID,
	}, branchFromForm(r), r.PostFormValue("source_branch"), r.PostFormValue("content"),
		r.PostFormValue("name"), r.PostFormValue("submission_id"))
	if err != nil {
		writeGuestServiceError(w, r, err)
		return
	}

	name := cleanGuestName(r.PostFormValue("name"))
	renderGuest(w, r, templ.GuestProposalSent(name))
}

// branchFromForm reads the selected branch. The submit buttons carry send_action so a native form
// post names the clicked button unambiguously: with two buttons named "branch", the hidden field
// would always win and "Send another version" could never create an alternative without the script.
// Autosave has no send_action and uses the hidden field the script keeps in sync.
func branchFromForm(r *http.Request) string {
	if action := r.PostFormValue("send_action"); action != "" {
		return action
	}
	branch := r.PostFormValue("branch")
	if branch == "" {
		return services.PrimaryBranch
	}
	return branch
}

// writeGuestServiceError maps service errors onto the statuses the guest surface has always used.
func writeGuestServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, services.ErrNoLink), errors.Is(err, services.ErrNoDraft), errors.Is(err, services.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, services.ErrNoChanges), errors.Is(err, services.ErrTooManyAlternatives), errors.Is(err, services.ErrInvalidSubmission):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, services.ErrEmptyContent):
		writeGuestValidationError(w, &validationError{message: err.Error(), status: http.StatusBadRequest})
	case errors.Is(err, services.ErrContentTooLong):
		writeGuestValidationError(w, &validationError{message: err.Error(), status: http.StatusRequestEntityTooLarge})
	default:
		fail500(w, r, "failed to save the guest draft", err)
	}
}

func writeGuestJSON(w http.ResponseWriter, value any) {
	writeJSONNoStore(w, value)
}

// writeGuestJSONError keeps the autosave endpoint's media type consistent on failures. The script
// checks response.ok before parsing, but a JSON error is easier to diagnose than an HTML page.
func writeGuestJSONError(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, "could not save the draft"
	switch {
	case errors.Is(err, services.ErrNoLink), errors.Is(err, services.ErrNoDraft), errors.Is(err, services.ErrNotFound):
		status, message = http.StatusNotFound, "this link is no longer available"
	case errors.Is(err, services.ErrContentTooLong):
		status, message = http.StatusRequestEntityTooLarge, err.Error()
	case errors.Is(err, services.ErrEmptyContent), errors.Is(err, services.ErrInvalidSubmission):
		status, message = http.StatusBadRequest, err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: message}); err != nil {
		log.Printf("write the guest error response: %v", err)
	}
}

// renderGuest buffers a standalone guest component before writing it, so a render error cannot emit
// half a page. The authoring surfaces have renderSurface for the same reason; guest pages are not
// authoring surfaces and must not get the navigation or the HTMX bundle appended to them.
func renderGuest(w http.ResponseWriter, r *http.Request, content page.Component) {
	var buf bytes.Buffer
	if err := content.Render(r.Context(), &buf); err != nil {
		fail500(w, r, "failed to render the shared page", err)
		return
	}
	writeHTML(w, buf.Bytes())
}

// cleanGuestName trims and bounds the optional attribution, defaulting to "Anonymous".
func cleanGuestName(raw string) string {
	name := strings.Join(strings.Fields(raw), " ")
	if name == "" {
		return "Anonymous"
	}
	if len([]rune(name)) > maxGuestName {
		name = string([]rune(name)[:maxGuestName])
	}
	return name
}

// writeGuestValidationError preserves the validator's 400/413 status and escapes its message.
func writeGuestValidationError(w http.ResponseWriter, err error) {
	var ve *validationError
	if !errors.As(err, &ve) {
		ve = &validationError{message: "invalid request", status: http.StatusBadRequest}
	}
	body := `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>Could not send</title></head><body><p>` + html.EscapeString(ve.message) + `</p>` +
		`<p><a href="/guest/edit">Go back</a></p></body></html>`
	w.WriteHeader(ve.status)
	writeHTML(w, []byte(body))
}

// guestSessionIssuerKey carries the function that converts a resolved link into a cookie.
//
// The key is defined here, in handlers, because the server layer already imports this package and
// the reverse import would be a cycle. The issuer itself is injected by the server's entry-route
// wrapper, which is the only layer that holds the signing secret.
type guestSessionIssuerKey struct{}

// guestSessionIssuer is the function the server layer provides to PrivateLinkEntryHandler.
type guestSessionIssuer func(w http.ResponseWriter, r *http.Request, session services.PrivateLinkSession) error

// WithGuestSessionIssuer attaches the issuer to a request context.
func WithGuestSessionIssuer(ctx context.Context, issuer guestSessionIssuer) context.Context {
	return context.WithValue(ctx, guestSessionIssuerKey{}, issuer)
}

func guestSessionIssuerFrom(ctx context.Context) (guestSessionIssuer, bool) {
	issuer, ok := ctx.Value(guestSessionIssuerKey{}).(guestSessionIssuer)
	return issuer, ok
}
