package handlers

import (
	"context"
	"time"
)

// GuestSession is the scoped identity a private link grants to its holder.
//
// It is deliberately not the author's session. It names one work and one link, and every guest
// route derives the work from it rather than from a request parameter -- a guest cannot name a
// different work even by crafting a form or a URL.
type GuestSession struct {
	LinkID    string
	PoemID    string
	ExpiresAt time.Time
}

type guestCtxKey int

const guestCtxKeySession guestCtxKey = iota

// WithGuestSession returns a context carrying the guest session.
func WithGuestSession(ctx context.Context, session GuestSession) context.Context {
	return context.WithValue(ctx, guestCtxKeySession, session)
}

// GuestSessionFrom returns the guest session on ctx, or false if there is none.
//
// No guest route may proceed without one. Handlers that need it call this first and answer 404 if
// it is absent, so a route accidentally reached outside the guest group cannot serve content.
func GuestSessionFrom(ctx context.Context) (GuestSession, bool) {
	session, ok := ctx.Value(guestCtxKeySession).(GuestSession)
	return session, ok
}
