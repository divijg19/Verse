package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	page "github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/divijg19/Verse/internal/linktoken"
	"github.com/divijg19/Verse/internal/origin"
	"github.com/divijg19/Verse/internal/presenters"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

const publicCSRFCookie = "verse_public_csrf"

// publicLinkResult is shared by the idempotent and rotate responses so the browser can distinguish
// a reused link from a newly issued one without parsing the token.
type publicLinkResult struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Created bool   `json:"created"`
}

// SharePublicLinkHandler returns the work's public submission link, minting one only when the work
// has none. Reuse changes nothing: the token already handed out stays live. A revoked or
// unrecoverable row is never silently repaired -- that would strand everyone holding the old URL.
func SharePublicLinkHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requireFormWorkID(w, r)
	if !ok {
		return
	}
	base, err := origin.FromRequest(r)
	if err != nil {
		http.Error(w, "could not build the public submission link", http.StatusBadRequest)
		return
	}
	key, ok := linktoken.KeyFrom(r.Context())
	if !ok {
		fail500(w, r, "link token key is not configured", errors.New("missing link token key"))
		return
	}
	token, linkID, created, err := services.GetOrCreatePublicLink(r.Context(), key, id)
	if err != nil {
		writePublicLinkError(w, r, err, "failed to create a public submission link")
		return
	}
	writeJSONNoStore(w, publicLinkResult{ID: linkID, URL: linkURL(base, token, publicLinkPath), Created: created})
}

// SharePublicLinkRotateHandler issues a fresh token on the same row. Submissions keep their link_id
// attribution and the old URL stops resolving immediately. This is the only repair path for a
// revoked or unrecoverable link.
func SharePublicLinkRotateHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requireFormWorkID(w, r)
	if !ok {
		return
	}
	linkID, ok := parseWorkID(r.FormValue("link"))
	if !ok {
		http.Error(w, "invalid link id", http.StatusBadRequest)
		return
	}
	base, err := origin.FromRequest(r)
	if err != nil {
		http.Error(w, "could not build the public submission link", http.StatusBadRequest)
		return
	}
	key, ok := linktoken.KeyFrom(r.Context())
	if !ok {
		fail500(w, r, "link token key is not configured", errors.New("missing link token key"))
		return
	}
	token, publicID, err := services.RotatePublicLink(r.Context(), key, id, linkID)
	if err != nil {
		writePublicLinkError(w, r, err, "failed to rotate a public submission link")
		return
	}
	writeJSONNoStore(w, publicLinkResult{ID: publicID, URL: linkURL(base, token, publicLinkPath), Created: true})
}

func writePublicLinkError(w http.ResponseWriter, r *http.Request, err error, message string) {
	switch {
	case errors.Is(err, services.ErrWorkNotPublic):
		http.Error(w, "only a published work can receive public submissions", http.StatusConflict)
	case errors.Is(err, services.ErrPublicLinkRevoked):
		http.Error(w, "the public link was revoked; rotate it for a fresh one", http.StatusConflict)
	case errors.Is(err, services.ErrLinkNotRecoverable):
		http.Error(w, "the public link cannot be recovered; rotate it", http.StatusConflict)
	case errors.Is(err, services.ErrNoPublicLink):
		http.Error(w, "no such public link", http.StatusNotFound)
	default:
		fail500(w, r, message, err)
	}
}

// SharePublicLinkRevokeHandler withdraws a public submission link.
func SharePublicLinkRevokeHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requireFormWorkID(w, r)
	if !ok {
		return
	}
	linkID, ok := parseWorkID(r.FormValue("link"))
	if !ok {
		http.Error(w, "missing or malformed link", http.StatusBadRequest)
		return
	}
	if err := services.RevokePublicLink(r.Context(), id, linkID); err != nil {
		if errors.Is(err, services.ErrNoPublicLink) {
			http.Error(w, "no such link", http.StatusNotFound)
			return
		}
		fail500(w, r, "failed to revoke a public submission link", err)
		return
	}
	writeJSONNoStore(w, struct {
		Revoked bool `json:"revoked"`
	}{Revoked: true})
}

// PublicSubmitHandler renders the anonymous submission form and issues a double-submit CSRF cookie.
func PublicSubmitHandler(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	link, err := services.ResolvePublicLink(r.Context(), token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	poem, err := services.GetPoem(r.Context(), link.PoemID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	csrf := make([]byte, 16)
	if _, err := rand.Read(csrf); err != nil {
		fail500(w, r, "failed to start a public submission", err)
		return
	}
	value := hex.EncodeToString(csrf)
	http.SetCookie(w, &http.Cookie{
		Name: publicCSRFCookie, Value: value, Path: "/p/", HttpOnly: true,
		Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(time.Hour.Seconds()),
	})
	title := presenters.WorkTitle(poem.Content, presenters.TitleWidthWide)
	renderPublic(w, r, templ.PublicSubmit(title, token, value, ""))
}

// PublicSubmitPOSTHandler appends one submission from an anonymous holder of the link.
func PublicSubmitPOSTHandler(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	link, err := services.ResolvePublicLink(r.Context(), token)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	cookie, err := r.Cookie(publicCSRFCookie)
	if err != nil || cookie.Value == "" || cookie.Value != r.PostFormValue("csrf") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	submission, err := services.CreatePublicSubmission(r.Context(), link,
		r.PostFormValue("name"), r.PostFormValue("content"))
	if err != nil {
		switch {
		case errors.Is(err, services.ErrNoPublicLink):
			http.NotFound(w, r)
		case errors.Is(err, services.ErrEmptyContent):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			fail500(w, r, "failed to record a public submission", err)
		}
		return
	}
	renderPublic(w, r, templ.PublicSubmitSent(submission.GuestName))
}

func renderPublic(w http.ResponseWriter, r *http.Request, content page.Component) {
	var buf bytes.Buffer
	if err := content.Render(r.Context(), &buf); err != nil {
		fail500(w, r, "failed to render the public submission page", err)
		return
	}
	writeHTML(w, buf.Bytes())
}
