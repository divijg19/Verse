package templ

import "context"

// Security context plumbing shared between the HTTP layer and the templates.
//
// This file lives in the templ package because templ is a leaf: it imports only the standard
// library and github.com/a-h/templ. Putting the context keys here lets internal/server write them
// and the templates read them without either importing the other, which would be a cycle.
//
// The values carried here are the CSRF synchroniser token and the session expiry. Neither is
// authority on its own: the CSRF token is only meaningful once recomputed from the authenticated
// session, and the session itself is validated by the HTTP layer before the context is populated.

type securityCtxKey int

const (
	securityCtxKeyCSRF securityCtxKey = iota
)

// WithCSRFToken returns a context carrying the CSRF token for template rendering.
func WithCSRFToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, securityCtxKeyCSRF, token)
}

// CSRFToken returns the CSRF token carried by ctx, or an empty string.
//
// An empty string is returned for any context not populated by the HTTP layer, so a component
// rendered outside an authenticated request cannot emit a token that would be accepted.
func CSRFToken(ctx context.Context) string {
	token, ok := ctx.Value(securityCtxKeyCSRF).(string)
	if !ok {
		return ""
	}
	return token
}

// hasError reports whether an error message should be displayed.
//
// Conditionals are expressed as Go helpers rather than inline templ expressions, matching the
// convention already used throughout this package (see navActiveValue and heatmapCellClass).
func hasError(message string) bool {
	return message != ""
}
