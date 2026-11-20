package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/origin"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// Author-side endpoints for private links. All of these sit inside the authenticated group and must
// pass CSRF: minting a capability to edit a work is as sensitive as publishing it.

// ShareLinkHandler mints a private link for one work and returns its URL once.
//
// The raw token is shown exactly here and never again. The database keeps only its hash, so listing
// links later cannot reproduce the URL -- by design. A screenshot or a password manager is how the
// author keeps it; a later visit to this page will offer only revocation.
func ShareLinkHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requireFormWorkID(w, r)
	if !ok {
		return
	}

	// The link is an authoring-service route, so it is built from the origin that served this
	// request. PUBLIC_BASE_URL is the reader's static site and is deliberately not consulted here:
	// a local checkout has no such setting, and a deployed one would produce a 404 on the reader.
	base, err := origin.FromRequest(r)
	if err != nil {
		// A bad Host is normally a hostile client, but a proxy that rewrites it to an internal name
		// would fail every mint with the same symptom. %q escapes control characters so a crafted
		// value cannot forge a second log line, and the message stays diagnosable.
		log.Printf("refused a private-link request with an invalid host %q", r.Host) // #nosec G706 -- %q escapes control characters before the value reaches the log
		http.Error(w, "could not build the private link address", http.StatusBadRequest)
		return
	}

	token, linkID, expiresAt, err := services.CreatePrivateLink(r.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			http.Error(w, "no such work", http.StatusNotFound)
			return
		}
		fail500(w, r, "failed to create a private link", err)
		return
	}

	shareJSON(w, struct {
		ID        string `json:"id"`
		URL       string `json:"url"`
		ExpiresAt string `json:"expiresAt"`
	}{
		ID:        linkID,
		URL:       guestLinkURL(base, token),
		ExpiresAt: expiresAt,
	})
}

// ShareLinkRevokeHandler invalidates one of the author's links.
//
// The link is scoped by the submitted work id as well as its own id, so a request that names a link
// belonging to another work is indistinguishable from a missing one.
func ShareLinkRevokeHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requireFormWorkID(w, r)
	if !ok {
		return
	}
	linkID, ok := parseWorkID(r.FormValue("link"))
	if !ok {
		http.Error(w, "missing or malformed link", http.StatusBadRequest)
		return
	}

	if err := services.RevokePrivateLink(r.Context(), id, linkID); err != nil {
		if errors.Is(err, services.ErrNoLink) {
			http.Error(w, "no such link", http.StatusNotFound)
			return
		}
		fail500(w, r, "failed to revoke a private link", err)
		return
	}

	shareJSON(w, struct {
		Revoked bool `json:"revoked"`
	}{Revoked: true})
}

// ShareProposalApplyHandler applies one guest suggestion to the author's draft.
//
// The write happens in ApplyWorkProposal, which applies the same retained-version rule as UpdatePoem
// inside the same transaction as the "applied" mark. Doing it there, rather than calling UpdatePoem
// and then marking separately, is what makes a double click harmless. There is no auto-merge: this
// handler is the only path from a suggestion to the work, and the author has to choose it.
func ShareProposalApplyHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requireFormWorkID(w, r)
	if !ok {
		return
	}
	proposalID, ok := parseWorkID(r.FormValue("proposal"))
	if !ok {
		http.Error(w, "missing or malformed proposal", http.StatusBadRequest)
		return
	}

	// The read, the write and the mark are one transaction in the service layer. A separate
	// pre-check here would only reintroduce the race the service exists to close.
	if err := services.ApplyWorkProposal(r.Context(), id, proposalID); err != nil {
		switch {
		case errors.Is(err, services.ErrNoProposal):
			// Also the response to a second click: the first apply already consumed the proposal.
			http.Error(w, "this proposal was already applied", http.StatusConflict)
		case errors.Is(err, services.ErrNotFound):
			http.Error(w, "no such work", http.StatusNotFound)
		default:
			fail500(w, r, "failed to apply a proposal", err)
		}
		return
	}

	w.Header().Set("HX-Redirect", "/poem/"+id+"/history")
	w.WriteHeader(http.StatusNoContent)
}

// privateLinkViews maps service rows to the Share screen's view model.
func privateLinkViews(ctx context.Context, poemID string) ([]templ.PrivateLinkView, error) {
	links, err := services.ListPrivateLinks(ctx, poemID)
	if err != nil {
		return nil, err
	}

	counts, err := services.CountProposalsByLink(ctx, poemID)
	if err != nil {
		return nil, err
	}

	now := clock.NowUTC()
	views := make([]templ.PrivateLinkView, 0, len(links))
	for _, link := range links {
		// Expired links drop out of the list without ceremony. The row stays for history, but the
		// capability is unreachable and there is nothing left for the author to act on.
		if !now.Before(link.ExpiresAt) {
			continue
		}
		revoked := link.RevokedAt != nil
		views = append(views, templ.PrivateLinkView{
			ID:            link.ID,
			PoemID:        link.PoemID,
			CreatedAt:     link.CreatedAt,
			ExpiresAt:     link.ExpiresAt,
			Revoked:       revoked,
			ProposalCount: counts[link.ID],
		})
	}
	return views, nil
}
