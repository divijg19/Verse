package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/handlers"
	"github.com/divijg19/Verse/internal/services"
	views "github.com/divijg19/Verse/templ"
)

// Guest sessions are the temporary, single-work identity a private link grants.
//
// They are deliberately separate from the author's session: a different cookie name, a different
// cookie path, a different signing domain, and a different middleware. A guest who somehow obtains
// an author cookie cannot use it on /guest/*, and an author who opens a guest link does not lose
// their own session. The two grants are not interchangeable in either direction.
//
// The token is signed with the same secret as the author session but over a domain-separated
// message, so a valid author token cannot be relabeled as a guest token. It carries the link and
// work identifiers, a random nonce for CSRF, and the expiry the server enforces; the raw private
// link token is never put in a cookie.
const (
	guestCookieName = "verse_guest"
	guestPath       = "/guest/"
	guestNonceLen   = 16
)

var errNoGuestSession = errors.New("no valid guest session")

// guestSession is the parsed, authenticated cookie value.
type guestSession struct {
	linkID    string
	poemID    string
	nonce     []byte
	expiresAt time.Time
}

// newGuestSession mints a guest session for one link.
func newGuestSession(linkID, poemID string, expiresAt time.Time) (*guestSession, error) {
	nonce := make([]byte, guestNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return &guestSession{linkID: linkID, poemID: poemID, nonce: nonce, expiresAt: expiresAt}, nil
}

// guestPayload serializes the session. Layout: link(16) || work(16) || nonce(16) || expiry(8).
func guestPayload(s *guestSession) ([]byte, error) {
	link, err := uuid.Parse(s.linkID)
	if err != nil {
		return nil, err
	}
	poem, err := uuid.Parse(s.poemID)
	if err != nil {
		return nil, err
	}

	payload := make([]byte, 0, 16+16+guestNonceLen+8)
	payload = append(payload, link[:]...)
	payload = append(payload, poem[:]...)
	payload = append(payload, s.nonce...)
	unix := s.expiresAt.Unix()
	expiry := make([]byte, 8)
	for i := range expiry {
		expiry[i] = byte((unix >> (56 - 8*i)) & 0xff)
	}
	payload = append(payload, expiry...)
	return payload, nil
}

// guestToken signs a guest session into a cookie value.
func (cfg *authConfig) guestToken(s *guestSession) (string, error) {
	payload, err := guestPayload(s)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, cfg.secret)
	mac.Write([]byte("guest|"))
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// parseGuestToken validates a cookie value and returns the session it carries.
func (cfg *authConfig) parseGuestToken(value string, now time.Time) (*guestSession, error) {
	dot := strings.IndexByte(value, '.')
	if dot <= 0 || dot == len(value)-1 {
		return nil, errNoGuestSession
	}

	payload, err := base64.RawURLEncoding.DecodeString(value[:dot])
	if err != nil || len(payload) != 16+16+guestNonceLen+8 {
		return nil, errNoGuestSession
	}

	mac := hmac.New(sha256.New, cfg.secret)
	mac.Write([]byte("guest|"))
	mac.Write(payload)
	want, err := base64.RawURLEncoding.DecodeString(value[dot+1:])
	if err != nil || !hmac.Equal(mac.Sum(nil), want) {
		return nil, errNoGuestSession
	}

	link, err := uuid.FromBytes(payload[:16])
	if err != nil {
		return nil, errNoGuestSession
	}
	poem, err := uuid.FromBytes(payload[16:32])
	if err != nil {
		return nil, errNoGuestSession
	}

	var expiry int64
	for _, b := range payload[32+guestNonceLen:] {
		expiry = expiry<<8 | int64(b)
	}
	expiresAt := time.Unix(expiry, 0)
	if !now.Before(expiresAt) {
		return nil, errNoGuestSession
	}

	return &guestSession{
		linkID:    link.String(),
		poemID:    poem.String(),
		nonce:     append([]byte(nil), payload[32:32+guestNonceLen]...),
		expiresAt: expiresAt,
	}, nil
}

// guestCSRF derives the synchroniser token for a guest session.
func (cfg *authConfig) guestCSRF(s *guestSession) string {
	mac := hmac.New(sha256.New, cfg.secret)
	mac.Write([]byte("guestcsrf|"))
	mac.Write(s.nonce)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// validGuestCSRF reports whether a submitted token matches this guest's session.
func (cfg *authConfig) validGuestCSRF(s *guestSession, submitted string) bool {
	want := cfg.guestCSRF(s)
	return subtle.ConstantTimeCompare([]byte(want), []byte(submitted)) == 1
}

// issueGuestSession writes the guest cookie.
//
// The path is /guest/ so the browser does not attach this credential to any authoring route. There
// is deliberately no cookie at /s/ -- the entry route exchanges the link token for the scoped
// session, and nothing else needs the credential there. The exchange is repeatable by design: the
// URL is a capability, so holding it is what grants access, not having used it once.
func (cfg *authConfig) issueGuestSession(w http.ResponseWriter, s *guestSession) error {
	token, err := cfg.guestToken(s)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     guestCookieName,
		Value:    token,
		Path:     guestPath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.expiresAt.Sub(clock.NowUTC()).Seconds()),
	})
	return nil
}

// guestSessionFromRequest returns the validated session for a request, or nil.
func (cfg *authConfig) guestSessionFromRequest(r *http.Request) *guestSession {
	cookie, err := r.Cookie(guestCookieName)
	if err != nil {
		return nil
	}
	s, err := cfg.parseGuestToken(cookie.Value, clock.NowUTC())
	if err != nil {
		return nil
	}
	return s
}

// requireGuest guards every /guest/* route.
//
// The signature is checked first, then the link's live state. The second check is what makes
// revocation immediate: a session issued before the author revoked the link keeps a valid signature
// and a future expiry, but stops working on the next request because the database no longer says the
// link is active. A work that has been soft-deleted is refused by the same lookup: PrivateLinkActive
// joins poems with deleted_at IS NULL, because a soft delete does not fire the schema's cascade.
//
// Failures are a bare 404 rather than a redirect or an explanation. The holder of a revoked or
// expired link is not entitled to learn why, and a 404 does not confirm that the link once existed.
func requireGuest(cfg *authConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s := cfg.guestSessionFromRequest(r)
			if s == nil {
				http.NotFound(w, r)
				return
			}

			poemID, expiresAt, err := services.PrivateLinkActive(r.Context(), s.linkID)
			if err != nil {
				// A revoked, expired or withdrawn link and a database outage must not look the same.
				// The former is an ordinary 404; the latter is exactly the silent failure this
				// application's logging rules were written to make impossible.
				if errors.Is(err, services.ErrNoLink) {
					http.NotFound(w, r)
					return
				}
				log.Printf("guest link lookup failed (request %s): %v", sanitizeLogValue(requestIDFrom(r)), err) // #nosec G706 -- sanitizeLogValue is the actual mitigation; gosec does not credit it
				http.Error(w, "failed to check the link", http.StatusInternalServerError)
				return
			}
			if poemID != s.poemID {
				http.NotFound(w, r)
				return
			}
			s.expiresAt = expiresAt

			ctx := views.WithCSRFToken(r.Context(), cfg.guestCSRF(s))
			ctx = handlers.WithGuestSession(ctx, handlers.GuestSession{
				LinkID:    s.linkID,
				PoemID:    s.poemID,
				ExpiresAt: expiresAt,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requireGuestCSRF validates the synchroniser token on a guest's unsafe requests.
//
// It applies the same three checks as requireCSRF -- session, Origin, synchroniser token -- against
// the guest cookie. The token derivation is deliberately different (guestcsrf| rather than csrf|),
// so a token minted for the author cannot be replayed here. Refusals go through refuseRequest for
// the same reason the author's do: a bare 403 with nothing in the log is expensive to diagnose.
//
// The cookie is parsed again here rather than read from the context because the nonce is not part of
// the context's GuestSession. requireGuest has already validated the same signature and the live
// link on this request, so this is a second check of the same value, not a weaker one.
func requireGuestCSRF(cfg *authConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
				next.ServeHTTP(w, r)
				return
			}

			s := cfg.guestSessionFromRequest(r)
			if s == nil {
				refuseRequest(w, r, cfg, "no guest session")
				return
			}
			if originIsCrossSite(r.Header.Get("Origin"), r) {
				refuseRequest(w, r, cfg, "cross-origin guest request")
				return
			}

			// PostFormValue reads the body only. FormValue would also accept ?csrf=..., which would
			// put a synchroniser token in a URL where proxies and Referer headers can record it.
			submitted := r.PostFormValue(csrfFieldName)
			if submitted == "" {
				submitted = r.Header.Get("X-CSRF-Token")
			}
			if !cfg.validGuestCSRF(s, submitted) {
				refuseRequest(w, r, cfg, "guest token mismatch")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// guestHeadersMiddleware keeps guest pages private and unindexable.
//
// These pages show a draft that is not on the reader's site, so a shared cache must not keep a copy
// and a crawler must never list one. Applied to the entry route and the whole /guest/ tree.
func guestHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}
