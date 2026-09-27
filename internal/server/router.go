package server

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/handlers"
)

// securityHeaders are applied to every response.
//
// The Content-Security-Policy permits inline styles because the templates currently carry 1,746
// lines of inline <style>. Extracting them into a stylesheet is tracked for v0.4.0-B, at which point
// 'unsafe-inline' can be dropped from style-src. script-src does NOT permit unsafe-inline, so the
// vendored htmx and the single vendored navigation script remain the only executable sources.
const securityHeaders = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'"

// healthHandler answers the platform's liveness probe, and reports readiness rather than merely
// liveness.
//
// The distinction is the point. Before this consulted the database it returned 200 unconditionally,
// so a total Postgres outage left the platform polling a service it could not serve: no restart, no
// alert, and a log full of reassuring 200s. A probe that cannot fail is worse than no probe, because
// it is read as evidence.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	if err := database.Ping(r.Context()); err != nil {
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

// requestIDHeaderMiddleware copies the request's correlation ID onto the response as X-Request-Id.
//
// Set before the handler runs, because a header written after the status line has been flushed is
// silently dropped. An absent ID is left absent rather than replaced with a placeholder: a client
// that sees a value will quote it, and a fabricated one sends them looking for a request that does
// not exist.
func requestIDHeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := middleware.GetReqID(r.Context()); id != "" {
			w.Header().Set("X-Request-Id", id)
		}
		next.ServeHTTP(w, r)
	})
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
	r.Use(middleware.Timeout(30 * time.Second))

	r.NotFound(notFoundHandler)
	r.MethodNotAllowed(methodNotAllowedHandler)

	// Infrastructure. Public by necessity: the platform health probe must reach /health without a
	// session, and the login page needs the stylesheet. Neither exposes content.
	r.MethodFunc(http.MethodGet, "/health", healthHandler)
	r.MethodFunc(http.MethodHead, "/health", healthHandler)
	r.Handle("/static/*", staticHandler())

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

// staticHandler serves the asset directory.
//
// The path is resolved against the process working directory, which the Dockerfile pins explicitly
// to /app. Embedding with go:embed was rejected: it would force the generated stylesheet to exist
// at compile time, coupling "go build" to the Tailwind step and breaking "go run" when the artifact
// is absent.
func staticHandler() http.Handler {
	root := os.Getenv("VERSE_STATIC_DIR")
	if root == "" {
		root = "static"
	}

	fs := http.FileServer(http.Dir(root))

	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		fs.ServeHTTP(w, r)
	}))
}
