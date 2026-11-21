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

// SharePublicLinkHandler mints an unauthenticated submission link for a published work.
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
	token, linkID, err := services.CreatePublicLink(r.Context(), key, id)
	if err != nil {
		if errors.Is(err, services.ErrWorkNotPublic) {
			http.Error(w, "only a published work can receive public submissions", http.StatusConflict)
			return
		}
		fail500(w, r, "failed to create a public submission link", err)
		return
	}
	shareJSON(w, struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}{ID: linkID, URL: publicLinkURL(base, token)})
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
	shareJSON(w, struct {
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
