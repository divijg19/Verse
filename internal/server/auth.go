package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	views "github.com/divijg19/Verse/templ"
)

// Authentication for the private Verse authoring application.
//
// Verse is single-author by design: there is no user table, no registration, no password reset,
// and no multi-tenancy. The schema has been single-tenant since the first commit and adding accounts
// would be inventing a product Verse is not.
//
// The control is deliberately small and dependency-free:
//
//	session cookie  signed opaque token, HMAC-SHA256, HttpOnly + Secure + SameSite=Lax
//	login secret    compared in constant time against VERSE_AUTHORIZATION
//	CSRF            synchroniser token derived from the session nonce, delivered as a
//	                hidden form field and recomputed server-side on every non-GET request
//
// Both layers must be configured. If the secrets are absent the server refuses to start rather than
// serving an unauthenticated authoring surface; there is deliberately no bypass flag, because a
// backdoor is a worse outcome than an inconvenient recovery procedure. Recovery from a forgotten
// passphrase is to rotate the Render environment variable and redeploy.

const (
	sessionCookieName = "verse_session"
	csrfCookieName    = "verse_csrf"
	csrfFieldName     = "csrf"

	// sessionTTL bounds how long a login remains valid. Single author, single device.
	sessionTTL = 8 * time.Hour

	// tokenNonceLen is the random component of a session token.
	tokenNonceLen = 16
)

var (
	// errNoSession indicates no valid session was presented.
	errNoSession = errors.New("no valid session")
)

// authConfig holds the validated authentication configuration.
type authConfig struct {
	authorization []byte // SHA-256 of the passphrase, for constant-time comparison
	secret        []byte // HMAC key for session and CSRF tokens

	// limiter bounds how often a caller may get the passphrase wrong. Held on the config so a
	// request does not construct one, and so the zero value of authConfig is never usable.
	limiter *rateLimiter
}

// minAuthSecretLen is the shortest VERSE_AUTH_SECRET accepted.
//
// The secret keys the HMAC over session and CSRF tokens, so its length is the brute-force cost of
// forging either. A short secret is rejected rather than padded or warned about: a deploy that
// cannot supply 32 bytes of secret cannot be trusted with an authoring surface.
//
// A secret shorter than this is treated exactly like an absent one, and both are reported by name.
const minAuthSecretLen = 32

// minPassphraseLen is the shortest VERSE_AUTHORIZATION accepted.
//
// This floor did not exist until v0.4.2, which made it the weaker of the pair: the HMAC secret
// needed 32 characters while the passphrase guarding the entire archive needed only one. That is
// backwards, and it matters more than usual here because the login route has no rate limiting
// history to rely on and, until this release, no rate limiting at all -- an unthrottled single
// shared secret is a guessing target rather than a passphrase.
//
// 16 characters is roughly what four ordinary words provide, which keeps it honest without forcing
// a memorable passphrase to look like machine output. There is no way to measure the entropy of a
// string, so this is a floor on length and nothing more; "aaaaaaaaaaaaaaaa" passes it.
const minPassphraseLen = 16

// loadAuthConfig reads and validates the authentication environment. It returns an error naming
// every missing or invalid variable so a misconfigured deploy fails loudly and actionably instead
// of silently serving an open application.
func loadAuthConfig() (*authConfig, error) {
	var missing []string

	// Trimmed before the length check, so a passphrase that is mostly whitespace is judged on what
	// the user actually has to type, not on the padding around it.
	authorization := strings.TrimSpace(os.Getenv("VERSE_AUTHORIZATION"))
	if len(authorization) < minPassphraseLen {
		missing = append(missing, "VERSE_AUTHORIZATION")
	}

	secret := strings.TrimSpace(os.Getenv("VERSE_AUTH_SECRET"))
	if len(secret) < minAuthSecretLen {
		missing = append(missing, "VERSE_AUTH_SECRET")
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf(
			"refusing to start: authentication is not configured (missing or too short: %s); "+
				"the authoring application must never be served unauthenticated. "+
				"VERSE_AUTHORIZATION must be at least %d characters and VERSE_AUTH_SECRET at least %d",
			strings.Join(missing, ", "), minPassphraseLen, minAuthSecretLen)
	}

	// Hash the passphrase so the comparison is over fixed-length digests. Comparing raw bytes with
	// ConstantTimeCompare leaks length through early return.
	authzDigest := sha256.Sum256([]byte(authorization))
	secretDigest := sha256.Sum256([]byte(secret))

	return &authConfig{
		authorization: authzDigest[:],
		secret:        secretDigest[:],
		limiter:       newRateLimiter(),
	}, nil
}

// session is a validated, authenticated authoring session.
//
// The session lives only in the HTTP layer. Templates receive the derived CSRF token and expiry
// through the templ package's context helpers, which is a leaf package and therefore cannot import
// this one without creating a cycle.
type session struct {
	nonce   []byte
	expires time.Time
}

// checkPassphrase compares the submitted passphrase against the configured one in constant time.
func (cfg *authConfig) checkPassphrase(submitted string) bool {
	given := sha256.Sum256([]byte(submitted))
	return subtle.ConstantTimeCompare(given[:], cfg.authorization) == 1
}

// newSession mints a session with a fresh random nonce.
func (cfg *authConfig) newSession(now time.Time) (*session, error) {
	nonce := make([]byte, tokenNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate session nonce: %w", err)
	}
	return &session{nonce: nonce, expires: now.Add(sessionTTL)}, nil
}

// sign produces base64url(HMAC-SHA256(key, message)) for the configured secret.
func (cfg *authConfig) sign(message []byte) string {
	mac := hmac.New(sha256.New, cfg.secret)
	mac.Write(message)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// token serializes a session into a signed opaque cookie value.
//
// Layout: base64url( nonce || expiryUnixBE64 || HMAC-SHA256(nonce || expiryUnixBE64) )
//
// The nonce makes every token unique even when two logins occur in the same second, so a token
// cannot be replayed to infer that a session was already used.
func (cfg *authConfig) token(s *session) string {
	// Big-endian 8-byte encoding of the expiry. The mask makes the narrowing explicit rather
	// than incidental: a Unix timestamp is positive and fits, and byte() truncates by definition.
	unix := s.expires.Unix()
	expiry := make([]byte, 8)
	for i := range expiry {
		expiry[i] = byte((unix >> (56 - 8*i)) & 0xff)
	}

	payload := make([]byte, 0, len(s.nonce)+len(expiry))
	payload = append(payload, s.nonce...)
	payload = append(payload, expiry...)

	return base64.RawURLEncoding.EncodeToString(payload) + "." + cfg.sign(payload)
}

// parseToken validates a cookie value and returns the session it encodes.
func (cfg *authConfig) parseToken(value string, now time.Time) (*session, error) {
	dot := strings.IndexByte(value, '.')
	if dot <= 0 || dot == len(value)-1 {
		return nil, errNoSession
	}

	// The payload is the nonce followed by the expiry. The MAC is carried alongside it, not inside
	// it, so the length check here is nonce + expiry only.
	payload, err := base64.RawURLEncoding.DecodeString(value[:dot])
	if err != nil || len(payload) != tokenNonceLen+8 {
		return nil, errNoSession
	}

	// Recompute over the presented payload and compare in constant time. This authenticates the
	// token without trusting any part of it, including the expiry.
	if !hmac.Equal([]byte(cfg.sign(payload)), []byte(value[dot+1:])) {
		return nil, errNoSession
	}

	nonce := payload[:tokenNonceLen]
	expiryBytes := payload[tokenNonceLen : tokenNonceLen+8]

	var expiry int64
	for _, b := range expiryBytes {
		expiry = expiry<<8 | int64(b)
	}

	if now.After(time.Unix(expiry, 0)) {
		return nil, errNoSession
	}

	return &session{nonce: nonce, expires: time.Unix(expiry, 0)}, nil
}

// csrf derives the synchroniser token for a session.
//
// Binding the token to the session nonce means an attacker cannot mint a CSRF token: the value is
// not attacker-supplied, it is recomputed server-side from the authenticated session.
func (cfg *authConfig) csrf(s *session) string {
	return cfg.sign(append([]byte("csrf|"), s.nonce...))
}

// sessionFromRequest returns the validated session for a request, or nil.
func (cfg *authConfig) sessionFromRequest(r *http.Request) *session {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil
	}
	s, err := cfg.parseToken(cookie.Value, time.Now())
	if err != nil {
		return nil
	}
	return s
}

// issueSession writes the session and CSRF cookies and attaches the session to the request context.
func (cfg *authConfig) issueSession(w http.ResponseWriter, r *http.Request) *session {
	s, err := cfg.newSession(time.Now())
	if err != nil {
		// A failure here means the system CSPRNG is unavailable. Do not fall back to anything weaker.
		http.Error(w, "could not establish session", http.StatusInternalServerError)
		return nil
	}

	csrf := cfg.csrf(s)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    cfg.token(s),
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	// HttpOnly, like the session cookie. Nothing in the browser needs to read it: the token reaches
	// the server in the form field, and the server recomputes it from the authenticated session
	// rather than trusting either copy. A script-readable cookie would only widen the blast radius
	// of a cross-site scripting bug, for no benefit.
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrf,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})

	return s
}

// clearSession expires both cookies.
//
// A browser matches a cookie for deletion on name, domain, and path, so the Secure, HttpOnly, and
// SameSite attributes need not match the originals. All are set unconditionally to keep this simple
// and to satisfy the linter honestly rather than by suppression.
func clearSession(w http.ResponseWriter) {
	for _, name := range []string{sessionCookieName, csrfCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
	}
}

// requireAuth rejects any request that does not present a valid session.
//
// Applied to every authoring and mutating route, including HTMX endpoints. An HTMX request is an
// ordinary HTTP request and receives no exemption.
func requireAuth(cfg *authConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s := cfg.sessionFromRequest(r)
			if s == nil {
				// Content-negotiate so an HTMX fragment request receives an HX-Redirect rather than an
				// HTML login page spliced into the screen region.
				if wantsHTMLRedirect(r) {
					w.Header().Set("HX-Redirect", "/login")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			ctx := views.WithCSRFToken(r.Context(), cfg.csrf(s))
			ctx = views.WithSessionExpiry(ctx, s.expires)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requireCSRF validates the synchroniser token on every unsafe method.
//
// SameSite=Lax alone does not protect against a top-level cross-site POST, so this is required
// rather than optional. The token is recomputed from the session, so a value supplied by an
// attacker cannot match.
func requireCSRF(cfg *authConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
				next.ServeHTTP(w, r)
				return
			}

			s := cfg.sessionFromRequest(r)
			if s == nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			// Reject a cross-origin request outright. This is belt-and-braces: the synchroniser token
			// is the real control, but an unexpected Origin on a same-origin form is a signal worth
			// refusing rather than logging.
			if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			expected := cfg.csrf(s)
			submitted := r.FormValue(csrfFieldName)
			if submitted == "" {
				submitted = r.Header.Get("X-CSRF-Token")
			}

			if subtle.ConstantTimeCompare([]byte(expected), []byte(submitted)) != 1 {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// sameOrigin reports whether an Origin header matches the request's own scheme and host.
func sameOrigin(origin string, r *http.Request) bool {
	trimmed := strings.TrimSuffix(origin, "/")
	host := r.Host
	// Compare against the Host header, which is what the browser used to reach us. Behind a proxy
	// the scheme may differ, so only the authority is compared.
	return strings.EqualFold(trimmed, "https://"+host) || strings.EqualFold(trimmed, "http://"+host)
}

// wantsHTMLRedirect reports whether the client expects a browser-style navigation.
func wantsHTMLRedirect(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" || r.Header.Get("Hx-Request") == "true"
}

// parsePositiveInt reads a positive integer environment value, returning a default when absent or
// malformed. Used for server tuning so a typo degrades to a safe value rather than failing boot.
func parsePositiveInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
