package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	views "github.com/divijg19/Verse/templ"
)

// ServerConfig is the validated runtime configuration for the authoring application.
type ServerConfig struct {
	Port              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
	MaxBodyBytes      int64
}

// LoadServerConfig reads and validates the server environment.
//
// Every value has a safe default, so a missing optional variable degrades rather than failing. The
// only hard requirement is authentication, enforced separately in loadAuthConfig.
func LoadServerConfig() ServerConfig {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}

	seconds := func(name string, fallback int) time.Duration {
		return time.Duration(parsePositiveInt(name, fallback)) * time.Second
	}

	return ServerConfig{
		Port:              port,
		ReadHeaderTimeout: seconds("SERVER_READ_HEADER_TIMEOUT_SEC", 10),
		ReadTimeout:       seconds("SERVER_READ_TIMEOUT_SEC", 30),
		WriteTimeout:      seconds("SERVER_WRITE_TIMEOUT_SEC", 60),
		IdleTimeout:       seconds("SERVER_IDLE_TIMEOUT_SEC", 120),
		ShutdownTimeout:   seconds("SERVER_SHUTDOWN_TIMEOUT_SEC", 15),
		MaxHeaderBytes:    parsePositiveInt("SERVER_MAX_HEADER_BYTES", 1<<20),
		// Caps the memory a single request body can consume. Verse's works are text, so 1 MiB is
		// generous and removes the unbounded-allocation path entirely.
		MaxBodyBytes: int64(parsePositiveInt("SERVER_MAX_BODY_BYTES", 1<<20)),
	}
}

// The login form is reachable without a session, so it cannot carry a session-bound synchroniser
// token. It is protected by a process-lifetime random secret instead: the token is derived from it,
// delivered in a cookie, and required back as a form field. A cross-site form replay cannot predict
// it, and SameSite=Lax keeps the cookie off cross-site requests. This is defense in depth for a
// form that grants access, not a substitute for the passphrase.
var (
	loginCSRFOnce   sync.Once
	loginCSRFSecret []byte
)

// loginCSRFToken mints the process-scoped token that protects the login form.
func (cfg *authConfig) loginCSRFToken() (string, error) {
	loginCSRFOnce.Do(func() {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return // stays nil; loginCSRFToken reports the failure
		}
		digest := sha256.Sum256(secret)
		loginCSRFSecret = digest[:]
	})

	if loginCSRFSecret == nil {
		return "", errors.New("system randomness unavailable")
	}
	return cfg.sign(append([]byte("login|"), loginCSRFSecret...)), nil
}

// loginPageHandler renders the unauthenticated entry surface.
//
// It is the only screen that does not use the authoring shell: an unauthenticated visitor has no
// session, and therefore no dashboard, library, or navigation to render.
func loginPageHandler(cfg *authConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderLogin(w, r, "", cfg)
	}
}

// loginSubmitHandler authenticates a submitted passphrase.
func loginSubmitHandler(cfg *authConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := verifyLoginCSRF(r); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if !cfg.checkPassphrase(r.FormValue("passphrase")) {
			// No distinction between "wrong" and "empty", and no artificial delay: the comparison is
			// over fixed-length SHA-256 digests, so there is no timing signal to harvest, and a
			// sleep would only be a denial-of-service vector.
			renderLogin(w, r, "That passphrase was not accepted.", cfg)
			return
		}

		if cfg.issueSession(w, r) == nil {
			return // issueSession already wrote a 500
		}

		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// logoutHandler clears the session.
//
// POST only, so a cross-site link or image cannot sign the author out.
func logoutHandler(w http.ResponseWriter, r *http.Request) {
	clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// verifyLoginCSRF validates a login submission's token and origin.
func verifyLoginCSRF(r *http.Request) error {
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) {
		return errBadCSRF
	}

	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return errBadCSRF
	}

	if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(r.FormValue(csrfFieldName))) != 1 {
		return errBadCSRF
	}

	return nil
}

// renderLogin writes the login page and its CSRF cookie.
func renderLogin(w http.ResponseWriter, r *http.Request, errMessage string, cfg *authConfig) {
	token, err := cfg.loginCSRFToken()
	if err != nil {
		log.Printf("mint login csrf token: %v", err)
		http.Error(w, "could not prepare login form", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})

	var buf bytes.Buffer
	if err := views.Login(r.Context(), token, errMessage).Render(r.Context(), &buf); err != nil {
		// Log the detail, never return it: an internal error string in a response body is
		// information disclosure.
		log.Printf("render login page: %v", err)
		http.Error(w, "could not render login page", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(buf.Bytes()); err != nil {
		log.Printf("write login page: %v", err)
	}
}
