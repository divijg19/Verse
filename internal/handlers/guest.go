package handlers

import (
	"bytes"
	"context"
	"errors"
	"html"
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

// guestLinkPath is the prefix of every shareable private link.
const guestLinkPath = "/s/"

// guestEditorPath is where an entry redirects and where the limited editor lives.
const guestEditorPath = "/guest/edit"

// PrivateLinkEntryHandler is the public entry for a private link.
//
// A token is exchanged for a short-lived, work-scoped session cookie and the browser is redirected
// to the guest editor, which drops the raw token from the address bar. The raw token is never put in
// a cookie or a log; only its hash is stored, and only the signed session id reaches the browser.
//
// Every failure is a bare 404. Whether a link expired, was revoked, or never existed is not
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

	remaining := guest.ExpiresAt.Sub(clock.NowUTC())
	if remaining < 0 {
		remaining = 0
	}

	page := templ.GuestEditor(templ.GuestEditorData{
		Title:     presenters.WorkTitle(poem.Content, presenters.TitleWidthWide),
		Content:   poem.Content,
		ExpiresAt: guest.ExpiresAt,
		Minutes:   int(remaining / time.Minute),
	})
	renderGuest(w, r, page)
}

// GuestProposalHandler records a guest's suggested rewrite.
//
// It never touches the author's text. On success the guest sees a confirmation; the proposal waits
// in the work's history until the author chooses to apply it.
func GuestProposalHandler(w http.ResponseWriter, r *http.Request) {
	guest, ok := GuestSessionFrom(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}

	content, err := validateContent(r.FormValue("content"))
	if err != nil {
		writeGuestValidationError(w, err)
		return
	}

	name := cleanGuestName(r.FormValue("name"))
	if err := services.CreateWorkProposal(r.Context(), services.PrivateLinkSession{
		LinkID: guest.LinkID,
		PoemID: guest.PoemID,
	}, name, content); err != nil {
		if errors.Is(err, services.ErrNoLink) {
			http.NotFound(w, r)
			return
		}
		fail500(w, r, "failed to record a proposal", err)
		return
	}

	renderGuest(w, r, templ.GuestProposalSent(name))
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

// writeGuestValidationError reports a rejected suggestion with the validator's own status.
//
// The author's forms use writeValidationError, which preserves the 400/413 distinction. The guest
// surface used to discard it and answer 200 with an error page, so a client could not tell a stored
// proposal from a rejected one. The message stays escaped for the same reason as everywhere else.
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
