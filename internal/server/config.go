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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/divijg19/Verse/internal/database"
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
		if rejection := verifyLoginCSRF(r); rejection != loginAccepted {
			logLoginRejection(r, rejection)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		// The limiter is consulted before the passphrase is compared, so a blocked caller is refused
		// without the comparison costing anything.
		//
		// A limiter that cannot reach the database is logged and ignored rather than allowed to lock
		// the author out of their own application. Failing closed here would mean the defense becomes
		// the outage.
		subject := subjectKey(r, cfg.secret)
		if until, blocked, err := cfg.limiter.blockedUntil(r.Context(), database.Pool, subject); err != nil {
			log.Printf("login rate limit unavailable, proceeding without it: %v", err)
		} else if blocked {
			retry := retryAfterSeconds(until.Sub(cfg.limiter.now()))
			log.Printf("login refused: rate limited, retry in %ds", retry)
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			http.Error(w, "too many failed attempts", http.StatusTooManyRequests)
			return
		}

		if !cfg.checkPassphrase(r.FormValue("passphrase")) {
			// No distinction between "wrong" and "empty", and no artificial delay: the comparison is
			// over fixed-length SHA-256 digests, so there is no timing signal to harvest, and a
			// sleep would only be a denial-of-service vector. The cost of guessing is imposed by the
			// rate limiter instead, which refuses quickly and does not hold the connection.
			if until, blocked, err := cfg.limiter.recordFailure(r.Context(), database.Pool, subject); err != nil {
				log.Printf("record login failure: %v", err)
			} else if blocked {
				retry := retryAfterSeconds(until.Sub(cfg.limiter.now()))
				log.Printf("login refused: rate limit reached after a failure, retry in %ds", retry)
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				http.Error(w, "too many failed attempts", http.StatusTooManyRequests)
				return
			}
			renderLogin(w, r, "That passphrase was not accepted.", cfg)
			return
		}

		// A correct passphrase clears the record, so a later slip is not punished for this one.
		if err := cfg.limiter.clear(r.Context(), database.Pool, subject); err != nil {
			log.Printf("clear login failures: %v", err)
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

// loginRejection names why a login submission was refused.
//
// These reasons are for the log, never for the response. The response is a bare 403 in every case,
// because a client that cannot produce a valid token has no business learning which of its three
// mistakes it made.
//
// The distinction exists because the refusals are not equally meaningful. A cross-origin submission
// is the attack this defense exists for. A missing cookie on a plain-HTTP request is a deployment
// fault -- a browser silently refuses to store a Secure cookie without HTTPS -- and looks identical
// from the outside. Reporting both as "forbidden" made that indistinguishable.
type loginRejection int

const (
	loginAccepted loginRejection = iota
	loginBadOrigin
	loginNoCookie
	loginNoTokenField
	loginTokenMismatch
)

func (r loginRejection) String() string {
	switch r {
	case loginAccepted:
		return "accepted"
	case loginBadOrigin:
		return "cross-origin submission"
	case loginNoCookie:
		return "no synchroniser cookie was sent"
	case loginNoTokenField:
		return "the form carried no synchroniser field"
	case loginTokenMismatch:
		return "the synchroniser cookie did not match the form"
	default:
		return "unrecognized reason"
	}
}

// verifyLoginCSRF validates a login submission's origin and synchroniser token.
func verifyLoginCSRF(r *http.Request) loginRejection {
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) {
		return loginBadOrigin
	}

	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return loginNoCookie
	}

	submitted := r.FormValue(csrfFieldName)
	if submitted == "" {
		// Kept separate from a mismatch on purpose. An absent field means the form did not render
		// one -- a template or deployment fault, visible in the log -- whereas a mismatch means a
		// token was sent and was wrong. Collapsing them hides the first, which is the one that is
		// ours rather than an attacker's.
		return loginNoTokenField
	}

	if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(submitted)) != 1 {
		return loginTokenMismatch
	}

	return loginAccepted
}

// requestScheme reports the scheme the client believes it is using.
//
// Render terminates TLS and forwards the original scheme in X-Forwarded-Proto, so r.TLS is nil even
// on a perfectly good HTTPS deployment. The header is used for reporting only; it never gates
// anything, so a forged value can mislead a log line and nothing else.
func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}

	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		return "http"
	}
	// X-Forwarded-Proto may be a list, client-first. The leftmost entry is the client's claim; the
	// first entry is the one every honest hop agrees on.
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	return strings.ToLower(strings.TrimSpace(proto))
}

// logLoginRejection records why a login was refused, without recording anything usable.
//
// The scheme is on every line because it is the fact that explains most of them. The specific case
// called out separately is a plain-HTTP request with no cookie: that is the signature of a browser
// discarding the Secure cookie, which is a deployment problem and reads as an attack otherwise.
func logLoginRejection(r *http.Request, rejection loginRejection) {
	scheme := requestScheme(r)

	if scheme == "http" && rejection == loginNoCookie {
		log.Printf("login refused: %s; request arrived over plain HTTP, so a browser would have "+
			"discarded the Secure %s cookie rather than this being an attack", rejection, csrfCookieName)
		return
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "(none)"
	}
	// Both values are attacker-controlled request headers, so both go through sanitizeLogValue,
	// which strips control characters and truncates. Without that, a client could put a newline in
	// an Origin header and forge log lines that look like the application's own -- and a log that can
	// be forged is worse than none, because it gets trusted during exactly the incident where
	// trusting it is tempting.
	//
	// #nosec G706 -- the suppression is for the taint rule only, and the sanitization above is the
	// actual mitigation. gosec flags the call on the strength of the argument being request-derived
	// and does not credit sanitizeLogValue for removing the control characters, so the finding
	// persists with the fix in place. Verified by removing this line: G706 fires either way.
	log.Printf("login refused: %s; scheme=%s origin=%s", // #nosec G706
		rejection, sanitizeLogValue(scheme), sanitizeLogValue(origin))
}

// maxLoggedHeader bounds a logged request header, so a client cannot fill the log with a megabyte
// of its own choosing in a line that is supposed to be a diagnostic.
const maxLoggedHeader = 120

// sanitizeLogValue makes a request-derived value safe to write to a log line.
//
// Origin and X-Forwarded-Proto are attacker-controlled: a client can put a newline in either and
// would otherwise be able to forge log entries that look like the application's own. A log that can
// be forged is worse than no log, because it is trusted during exactly the incident where trusting
// it is tempting.
//
// Control characters are removed rather than escaped so the line stays readable, and the result is
// truncated so its length cannot be chosen either. Neither affects the comparison the value took
// part in: this runs on the reporting path only.
func sanitizeLogValue(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)

	if len(cleaned) > maxLoggedHeader {
		cleaned = cleaned[:maxLoggedHeader] + "..."
	}
	return cleaned
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
