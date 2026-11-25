package handlers

import (
	"context"
	"log"
	"net/http"

	"github.com/divijg19/Verse/internal/linktoken"
	"github.com/divijg19/Verse/internal/origin"
	"github.com/divijg19/Verse/internal/presenters"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// The maximum number of works the Share picker offers.
//
// The picker is for sharing a work, not for browsing the library -- that is what the Library screen
// is for. A bounded list keeps the page light and keeps the action on its purpose.
const shareWorkLimit = 100

// The Share overlay shows the newest suggestions, not the whole record: a single submission can be
// 200,000 runes and this payload is fetched on every picker change and link mutation. History keeps
// the unbounded record; the overlay states the total from the per-link counts when it truncates.
const shareSubmissionLimit = 200

// ShareHandler renders the Share surface and supports HTMX partial responses.
//
// The picker needs the author's works, so this handler reads them. The bodies are deliberately not
// read here: the page holds titles and identifiers only, and the endpoint below supplies one work
// when an action actually needs its text.
func ShareHandler(w http.ResponseWriter, r *http.Request) {
	works, err := services.ListPoems(r.Context(), shareWorkLimit, 0)
	if err != nil {
		fail500(w, r, "failed to list works for sharing", err)
		return
	}

	options := make([]templ.ShareWorkOption, 0, len(works))
	for _, poem := range works {
		options = append(options, templ.ShareWorkOption{
			ID:        poem.ID,
			Title:     presenters.WorkTitle(poem.Content, presenters.TitleWidthWide),
			Published: poem.PublishedAt != nil,
		})
	}

	renderSurface(w, r, "share", templ.Share(options, shareAddress() != ""))
}

// ShareWorkHandler returns one work's share data as JSON.
//
// It exists because the actions need the body and the public address, and neither belongs in the
// picker's markup: the body is the author's work verbatim, and embedding every body would put a copy
// of the whole library into a page that is meant to hold a list of names.
func ShareWorkHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requirePathWorkID(w, r)
	if !ok {
		return
	}

	poem, err := services.GetPoem(r.Context(), id)
	if err != nil {
		writePoemFetchError(w, r, id, err)
		return
	}

	state, err := services.PublicationState(r.Context(), id)
	if err != nil {
		writePoemFetchError(w, r, id, err)
		return
	}

	// Without PUBLIC_BASE_URL there is no reader origin to build an absolute address on. Returning a
	// relative /w/... used to make Copy Link hand out a path the clipboard could not resolve; leaving
	// it empty lets the client say the address is not configured instead.
	baseURL := shareAddress()
	address := ""
	if state.Published && baseURL != "" {
		address = baseURL + "/w/" + poem.ID + "/"
	}
	publicAddressConfigured := baseURL != ""

	published := ""
	if state.PublishedAt != nil {
		published = state.PublishedAt.UTC().Format("2006-01-02")
	}

	// The authoring origin builds both submission links. It is request-derived (or set by
	// VERSE_AUTHOR_BASE_URL), never PUBLIC_BASE_URL, which names the reader and has no /s or /p route.
	// A bad host means no Copy buttons, not a failed share payload.
	authorBase, originErr := origin.FromRequest(r)
	if originErr != nil {
		authorBase = ""
	}
	linkKey, _ := linktoken.KeyFrom(r.Context())

	// Private links are an addition to this endpoint, not a precondition for the three actions that
	// were here first. If the new tables are unavailable -- a deployment whose grants have not been
	// applied is the realistic case -- the share data still answers with an empty link list rather
	// than taking Copy Text, Copy Link and Card Export down with it.
	links, err := privateLinkViews(r.Context(), linkKey, authorBase, id)
	if err != nil {
		log.Printf("failed to list private links for %s: %v", id, err) // #nosec G706 -- id was parsed as a UUID by requirePathWorkID
		links = nil
	}

	publicLinks, err := publicLinkViews(r.Context(), linkKey, authorBase, id, state.Published)
	if err != nil {
		log.Printf("failed to list public links for %s: %v", id, err) // #nosec G706 -- validated UUID
		publicLinks = nil
	}

	// The overlay is an addition to this endpoint, not a precondition for the actions that were
	// here first. A failed submission read degrades to an empty list, as the two link reads do.
	recent, err := services.ListRecentPublicSubmissions(r.Context(), id, shareSubmissionLimit)
	if err != nil {
		log.Printf("failed to list public submissions for %s: %v", id, err) // #nosec G706 -- validated UUID
		recent = nil
	}
	submissions := publicSubmissionViews(recent)

	body := presenters.BodyWithoutTitle(poem.Content)
	out := struct {
		Title                   string                       `json:"title"`
		Body                    string                       `json:"body"`
		Address                 string                       `json:"address"`
		Published               string                       `json:"published"`
		IsPublished             bool                         `json:"isPublished"`
		PublicAddressConfigured bool                         `json:"publicAddressConfigured"`
		Links                   []templ.PrivateLinkView      `json:"links"`
		PublicLinks             []templ.PublicLinkView       `json:"publicLinks"`
		PublicSubmissions       []templ.PublicSubmissionView `json:"publicSubmissions"`
	}{
		Title:                   presenters.WorkTitle(poem.Content, presenters.TitleWidthWide),
		Body:                    body,
		Address:                 address,
		Published:               published,
		IsPublished:             state.Published,
		PublicAddressConfigured: publicAddressConfigured,
		Links:                   links,
		PublicLinks:             publicLinks,
		PublicSubmissions:       submissions,
	}

	writeJSONNoStore(w, out)
}

// publicSubmissionViews maps stored submissions for both the History surface and the Share
// overlay. One projection keeps the two read-only surfaces from drifting when a field is added.
func publicSubmissionViews(submissions []services.PublicSubmission) []templ.PublicSubmissionView {
	views := make([]templ.PublicSubmissionView, 0, len(submissions))
	for _, submission := range submissions {
		views = append(views, templ.PublicSubmissionView{
			ID:        submission.ID,
			LinkID:    submission.LinkID,
			GuestName: submission.GuestName,
			Content:   submission.Content,
			CreatedAt: submission.CreatedAt,
		})
	}
	return views
}

// publicLinkViews maps public links and their submission counts for the Share screen.
//
// Recoverability is computed even when no URL can be emitted, so a legacy row or a rotated key is
// reported as unrecoverable rather than being mistaken for a revoked or unpublished link.
func publicLinkViews(ctx context.Context, key linktoken.Key, base, poemID string, published bool) ([]templ.PublicLinkView, error) {
	links, err := services.ListPublicLinks(ctx, poemID)
	if err != nil {
		return nil, err
	}
	counts, err := services.CountPublicSubmissionsByLink(ctx, poemID)
	if err != nil {
		return nil, err
	}
	views := make([]templ.PublicLinkView, 0, len(links))
	for _, link := range links {
		revoked := link.RevokedAt != nil
		recoverable := false
		var token string
		if link.TokenSeed.Valid {
			token, err = linktoken.Recover(key, linktoken.Public, link.TokenSeed.String, link.TokenHash)
			recoverable = err == nil
		}
		state := linkState(revoked, false, published, recoverable)

		url := ""
		if state == templ.LinkStateActive && base != "" {
			url = linkURL(base, token, publicLinkPath)
		}
		views = append(views, templ.PublicLinkView{
			ID:              link.ID,
			PoemID:          link.PoemID,
			CreatedAt:       link.CreatedAt,
			Revoked:         revoked,
			SubmissionCount: counts[link.ID],
			State:           string(state),
			Copyable:        url != "",
			URL:             url,
		})
	}
	return views, nil
}
