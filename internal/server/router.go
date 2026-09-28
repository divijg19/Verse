package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/handlers"
)

// securityHeaders are applied to every response.
//
// The Content-Security-Policy permits inline styles because the templates carry inline <style>.
// Extracting them into a stylesheet is tracked for v0.4.0-B, at which point 'unsafe-inline' can be
// dropped from style-src.
//
// script-src does NOT permit unsafe-inline, and there is no nonce and no hash anywhere in this
// application. That is a deliberate policy and it stays: an inline <script> or an on*= attribute in
// the markup would be blocked.
//
// It was also, until v0.4.8, a policy the application violated on every page it served. The previous
// version of this comment claimed that the policy meant "the vendored htmx and the single vendored
// navigation script remain the only executable sources", which is how the violation was read as
// intended behavior for eleven releases. The editor shipped a 75-line inline <script> and six
// inline on*= attributes; the mobile navigation shipped two more. All of them were blocked in every
// browser, so the editor's Focus Mode never opened and the mobile navigation -- the only route to
// the nav below 1024px -- did nothing. Nothing in the test suite related the policy to the markup, so
// the contradiction was invisible: one test asserted the header string and another asserted the
// markup, and no assertion joined them.
//
// The code moved rather than the policy loosening. Adding 'unsafe-inline' would have made the header
// true by making it useless. TestContentSecurityPolicyMatchesTheMarkup is the assertion that was
// missing, and it is the reason this cannot recur.
//
// The line count is deliberately not quoted here. It was quoted once, drifted, and correcting it
// produced a second wrong number: three different counting methods gave three different answers for
// the same tree. A figure in a security rationale that nobody recomputes is worse than none, because
// it reads as a measurement. The claims that matter are the causal one above and the violation that
// followed from it, and both are now asserted.
//
// RequestTimeout bounds any single request, whatever it is doing. It is the ceiling on how long
// anything in this service can hold a scarce resource, so it is a named constant rather than a
// literal: HealthPingTimeout exists to stay well under it, and that relationship is asserted by a
// test.
const RequestTimeout = 30 * time.Second

const securityHeaders = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'"

// healthPingTimeout bounds the database check behind /health.
//
// Deliberately far shorter than the router's 30s request timeout, and the reason is the connection
// pool rather than the probe. The pool holds five connections by default, Render probes roughly every
// five seconds, and pgx holds a connection for the whole of a Ping. Inheriting the request timeout
// therefore allowed up to six probes to be in flight against five connections: if the database was
// slow but alive -- a Neon compute resuming, added latency -- every application query would then
// block waiting for a connection and be killed by the same 30s timeout, turning a slow database into
// a total outage. Before this endpoint consulted the database at all, that mode did not exist.
//
// 1500ms is a quarter of the platform's own five-second check window, so a ping slower than this was
// already a failed check. Holding a scarce connection for six times that bought nothing.
//
// Exported so a test can pin the relationship against RequestTimeout. Asserting it through timing
// alone would be a slow, flaky test that eventually gets deleted, and the relationship is the whole
// point of the constant.
const HealthPingTimeout = 1500 * time.Millisecond

// healthHandler answers the platform's liveness probe, and reports readiness rather than merely
// liveness.
//
// The distinction is the point. Before this consulted the database it returned 200 unconditionally,
// so a total Postgres outage left the platform polling a service it could not serve: no restart, no
// alert, and a log full of reassuring 200s.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	// The probe's own deadline, not the request's. See healthPingTimeout.
	ctx, cancel := context.WithTimeout(r.Context(), HealthPingTimeout)
	defer cancel()

	if err := database.Ping(ctx); err != nil {
		// #nosec G706 -- request-derived; sanitized as in refuseRequest. The error is included
		// because it is the only place the connection failure is recorded.
		log.Printf("health: database unreachable (request %s): %v", // #nosec G706
			sanitizeLogValue(requestIDFrom(r)), err)
		w.WriteHeader(http.StatusServiceUnavailable)
		if r.Method == http.MethodGet {
			// The reason is logged but not returned. /health is unauthenticated, and a connection
			// error carries the database host and user, which is reconnaissance for anyone who can
			// reach the probe.
			writeHealthBody(w, "unavailable")
		}
		return
	}

	w.WriteHeader(http.StatusOK)

	if r.Method == http.MethodGet {
		writeHealthBody(w, "ok")
	}
}

// writeHealthBody writes the probe's body, recording a short write rather than discarding it.
func writeHealthBody(w http.ResponseWriter, body string) {
	if _, err := w.Write([]byte(body)); err != nil {
		// The platform probe has already received its status line; a short write is not actionable,
		// but it is still worth recording.
		log.Printf("health response write: %v", err)
	}
}

// notFoundHandler returns a plain response for unknown routes. Without this, a mistyped
// authenticated path would render chi's default text body inside the application shell.
func notFoundHandler(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not found", http.StatusNotFound)
}

// methodNotAllowedHandler returns a plain response for unsupported methods.
func methodNotAllowedHandler(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// inboundRequestIDMiddleware bounds the caller's X-Request-Id before anything else reads it.
//
// This runs before middleware.RequestID, and that ordering is the whole point. RequestID honors an
// inbound value verbatim, and from there the value reaches two places: chi's request logger, which
// writes it into the log line, and the response header below. Sanitizing on the way out would have
// bounded only the response and left the log forgeable -- a caller could write arbitrary text into
// this service's logs, which is worse than having no log at all because the result gets trusted
// during exactly the incident where trusting it is tempting.
//
// Sanitizing once, here, means the two surfaces cannot disagree about what the value is. The
// cost is that a caller sending an over-long ID gets a truncated one back, which is the intended
// behavior: the ID exists to correlate, and a correlation handle is not worth a megabyte of
// caller-supplied text.
//
// Rewriting the request header rather than only the context is deliberate, since it is the header
// that chi reads.
func inboundRequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get(requestIDHeader); id != "" {
			if cleaned := sanitizeHeaderValue(id); cleaned != id {
				r.Header.Set(requestIDHeader, cleaned)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requestIDHeaderMiddleware copies the request's correlation ID onto the response as X-Request-Id.
//
// Set before the handler runs, because a header written after the status line has been flushed is
// silently dropped. An absent ID is left absent rather than replaced with a placeholder: a client
// that sees a value will quote it, and a fabricated one sends them looking for a request that does
// not exist.
//
// The value needs no further sanitizing -- inboundRequestIDMiddleware has already bounded it on the
// way in -- and this copies what chi resolved, so an inbound ID and a generated one are echoed
// identically.
func requestIDHeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := middleware.GetReqID(r.Context()); id != "" {
			w.Header().Set(requestIDHeader, id)
		}
		next.ServeHTTP(w, r)
	})
}

// requestIDHeader is the header carrying the correlation ID, in both directions.
const requestIDHeader = "X-Request-Id"

// maxRequestIDValue bounds a caller-supplied correlation ID.
//
// Generous for a platform request ID and small enough that a caller cannot turn every log line and
// every response into a megabyte of their own text. Separate from maxLoggedHeader, which bounds what
// the application logs: these are different surfaces with different costs, and conflating them would
// either over-truncate the log or under-bound the value at the edge.
const maxRequestIDValue = 200

// sanitizeHeaderValue strips control characters and truncates, for a value that came from a request.
func sanitizeHeaderValue(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(cleaned) > maxRequestIDValue {
		cleaned = cleaned[:maxRequestIDValue] + "..."
	}
	return cleaned
}

// requestIDFrom returns the request's correlation ID for log lines, or "-" when there is none.
//
// Log output is not a response, so a placeholder is safe here where it would not be in a header.
func requestIDFrom(r *http.Request) string {
	if id := middleware.GetReqID(r.Context()); id != "" {
		return id
	}
	return "-"
}

// securityHeadersMiddleware sets hardening headers on every response.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", securityHeaders)
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		// HSTS, and the reason it is here rather than merely noted. The session and synchroniser
		// cookies are both Secure, and a browser silently discards a Secure cookie on a page reached
		// over plain HTTP. The symptom is a login that renders and then answers 403 with no
		// explanation, which cost a debugging session before this header was understood to be the
		// fix rather than the diagnosis.
		//
		// HSTS is ignored over plain HTTP by design, so it can only take effect once a request has
		// already arrived over TLS. It does not prevent the first downgrade; it prevents every one
		// after that, which is where the repeated failures come from.
		//
		// One year, and deliberately without includeSubDomains or the preload directive. Both are
		// commitments about other hostnames, and this service is a single host on a platform that
		// serves a wildcard domain; neither belongs to this repository to make.
		h.Set("Strict-Transport-Security", "max-age=31536000")
		// The authoring application is private and must never be cached by a shared proxy.
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// bodyLimitMiddleware caps request body size, removing the unbounded-allocation path.
func bodyLimitMiddleware(max int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, max)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// NewRouter builds the HTTP route map used by the Verse authoring application.
//
// The route map is the security boundary. Every authoring surface and every mutating endpoint,
// including all HTMX endpoints, lives inside the authenticated group. Only /health and the static
// asset directory are reachable without a session.
//
// An HTMX request is an ordinary HTTP request and receives no exemption here.
//
// A missing or misconfigured authentication environment is returned as an error rather than
// panicking. Both refuse to start the service, but an error lets the caller emit one clear line
// naming the missing variables, where a panic emits a stack trace from a library function and
// obscures the actual cause. Recovery is to set the variables and redeploy; there is no bypass flag.
func NewRouter() (*chi.Mux, error) {
	authCfg, err := loadAuthConfig()
	if err != nil {
		return nil, err
	}

	cfg := LoadServerConfig()

	r := chi.NewRouter()

	// First, so nothing downstream sees an unbounded caller-supplied value. middleware.RequestID
	// honors an inbound X-Request-Id verbatim, and from there the value reaches both the log line
	// and the response.
	r.Use(inboundRequestIDMiddleware)
	r.Use(middleware.RequestID)
	// Echoed onto the response so a client can quote it, and so an application log line can be found
	// in the platform's own request log. middleware.RequestID honors an inbound X-Request-Id, so on
	// Render this is already the platform's identifier rather than one minted here -- which is the
	// only reason the two logs can be joined.
	r.Use(requestIDHeaderMiddleware)
	// middleware.RealIP is deliberately NOT used. It rewrites r.RemoteAddr to the leftmost
	// X-Forwarded-For value, or to True-Client-IP / X-Real-IP, without verifying that the request
	// actually passed through a trusted proxy. chi marks it deprecated for exactly this reason
	// (GHSA-3fxj-6jh8-hvhx, GHSA-rjr7-jggh-pgcp, GHSA-9g5q-2w5x-hmxf). Because the request logger
	// records r.RemoteAddr, enabling it would let any client write an arbitrary address into the
	// logs. The unrewritten peer address is the honest one.
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(securityHeadersMiddleware)
	r.Use(bodyLimitMiddleware(cfg.MaxBodyBytes))
	r.Use(middleware.Timeout(RequestTimeout))

	r.NotFound(notFoundHandler)
	r.MethodNotAllowed(methodNotAllowedHandler)

	// Infrastructure. Public by necessity: the platform health probe must reach /health without a
	// session, and the login page needs the stylesheet. Neither exposes content.
	r.MethodFunc(http.MethodGet, "/health", healthHandler)
	r.MethodFunc(http.MethodHead, "/health", healthHandler)

	// An explicitly configured root is validated here, so a deployment that points the variable at
	// something unusable stops with a message naming it rather than serving a 404 for every asset.
	//
	// The default is deliberately not validated, and the asymmetry is worth stating. "static" is
	// resolved against the working directory, so its absence means the process was started from
	// somewhere other than the project root -- which is a normal thing to do with a built binary, and
	// one the allowlist already handles correctly, since an unmatched path 404s whatever the root
	// happens to be. Failing on it would make NewRouter depend on the filesystem for a reason that has
	// nothing to do with routing, which is exactly the coupling several callers here do not expect.
	assetRoot := "static"
	if configured := os.Getenv("VERSE_STATIC_DIR"); configured != "" {
		assetRoot = configured
		// #nosec G703 -- the value is the operator's own configuration, read once at startup, and this
		// stat is the validation rather than a use. It is not request-derived: nothing a client sends
		// reaches this line, and the handler below never concatenates a request path onto a root --
		// it looks the request up in a map built from staticAssets, whose values are joined here.
		//
		// The check is worth its own failure modes, which are the two a misconfigured deployment
		// actually produces: a path that does not exist, and a path that is a file. Both are reported
		// by name, because a running service whose every asset 404s is a silent failure and a message
		// naming the variable is the whole value of checking.
		info, err := os.Stat(assetRoot)
		switch {
		case err != nil:
			return nil, fmt.Errorf("VERSE_STATIC_DIR %q is not readable: %w", assetRoot, err)
		case !info.IsDir():
			return nil, fmt.Errorf("VERSE_STATIC_DIR %q is not a directory", assetRoot)
		}
	}
	r.Handle("/static/*", staticHandler(assetRoot))

	// Authentication surface. Reachable without a session, and rate-limited only by the edge.
	r.Get("/login", loginPageHandler(authCfg))
	r.Post("/login", loginSubmitHandler(authCfg))

	// The authoring application. Everything below requires a valid session.
	r.Group(func(priv chi.Router) {
		priv.Use(requireAuth(authCfg))
		priv.Use(requireCSRF(authCfg))

		priv.Post("/logout", logoutHandler)

		// Primary surfaces.
		priv.Get("/", handlers.DashboardHandler)
		priv.Get("/dashboard", handlers.DashboardHandler)
		priv.Get("/editor", handlers.EditorHandler)
		priv.Get("/caelum", handlers.CaelumHandler)
		priv.Get("/library", handlers.LibraryHandler)
		priv.Get("/share", handlers.ShareHandler)

		// Work and editor routes.
		priv.Get("/poems", handlers.PoemsHandler)
		priv.Get("/poem/{id}", handlers.PoemViewHandler)
		priv.Get("/editor/{id}", handlers.EditorEditHandler)

		// Work lifecycle endpoints.
		priv.Post("/poem", handlers.SavePoemHandler)
		priv.Post("/poem/update", handlers.UpdatePoemHandler)
		priv.Post("/poem/delete", handlers.DeletePoemHandler)
		// History and restore. The route sits inside the authenticated group like every other
		// mutation, and the retained versions are as sensitive as the work itself.
		priv.Get("/poem/{id}/history", handlers.PoemHistoryHandler)
		priv.Post("/poem/restore", handlers.RestorePoemVersionHandler)

		// The recycle, and undelete. deleted_at was a one-way trip before this: the row survived,
		// so an accidental deletion was recoverable by hand in SQL and by no other means.
		priv.Get("/recycle", handlers.PoemTrashHandler)

		// The whole body of work, as a download. Inside the authenticated group: serving this
		// publicly would publish the entire library, which is the opposite of this service's
		// purpose. A GET, and safe as one, because it changes nothing.
		priv.Get("/export", handlers.ExportHandler)
		priv.Head("/export", handlers.ExportHandler)
		priv.Post("/poem/undelete", handlers.RestoreDeletedPoemHandler)

		// Optional prompt endpoint.
		priv.Get("/prompt", handlers.PromptHandler)
	})

	return r, nil
}

// staticAssets is every file this application serves, and the completeness of the list is the point.
//
// It replaces an http.FileServer over the asset directory, which had three exposures and no test
// could see them:
//
//   - A request for a directory returned a listing, so an unauthenticated visitor was told the exact
//     asset layout by asking for /static/, /static/js/ or /static/css/.
//   - /static/js/VENDOR.md and /static/js/VENDOR.sha256 were served, disclosing the vendored library's
//     version, license and digest to anyone.
//   - VERSE_STATIC_DIR was honored as given. Pointed at "." -- a plausible mistake -- the same
//     handler would have served /static/.env.
//
// An allowlist removes all three at once, and it removes the third structurally rather than by
// checking: a request is either one of these exact strings or it is not served, so no traversal,
// no symlink and no misconfigured root can reach a file that is not named here. The path is never
// joined onto the root and then validated afterwards; it is matched, and only then opened.
//
// Two files in the asset directory are deliberately absent. input.css is a build input, not an
// asset, and publishing it tells a reader which stylesheet framework and version produced the page.
// VENDOR.md and VENDOR.sha256 exist to be read by a human doing a supply-chain check, which is not
// the same thing as being served to a browser.
//
// Adding an asset means adding it here, and the test in tests/static_files_test.go fails on any file
// under static/ that is neither listed nor explicitly excluded, so this list cannot drift from the
// directory without someone being told.
//
// The values are relative to the asset root and are resolved against it once, at router
// construction, into the map the handler consults. The handler therefore never concatenates anything
// from a request with anything from the filesystem: a request supplies a key, and the value that
// comes back was written in this file. That is what makes traversal impossible rather than unlikely,
// and it is also why the linter's path-traversal analysis has nothing to follow.
var staticAssets = map[string]string{
	"css/output.css":   "css/output.css",
	"js/editor.js":     "js/editor.js",
	"js/htmx.min.js":   "js/htmx.min.js",
	"js/navigation.js": "js/navigation.js",
}

// staticHandler serves the asset directory, and only the assets named in staticAssets.
//
// The path is resolved against the process working directory, which the Dockerfile pins explicitly to
// /app. Embedding with go:embed was rejected: it would force the generated stylesheet to exist at
// compile time, coupling "go build" to the Tailwind step and breaking "go run" when the artifact is
// absent.
func staticHandler(root string) http.Handler {
	allowed := make(map[string]string, len(staticAssets))
	for name, rel := range staticAssets {
		allowed[name] = filepath.Join(root, rel)
	}

	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only reads are meaningful. A static asset has no side effect, and refusing the rest keeps
		// the surface exactly as small as the allowlist already makes it.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// StripPrefix has already removed the mount point. Leading slashes are trimmed so that the
		// comparison is against the same form staticAssets uses, and the result is matched against a
		// fixed set -- not cleaned, not joined and then checked. Nothing derived from the request
		// reaches the filesystem unless it is one of these strings.
		name := strings.TrimPrefix(r.URL.Path, "/")
		file, ok := allowed[name]
		if !ok {
			// Not 403: the caller is not forbidden from a thing that exists, and saying so would
			// confirm which assets are deployed.
			http.NotFound(w, r)
			return
		}

		if r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		http.ServeFile(w, r, file)
	}))
}
