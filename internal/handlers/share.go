package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/divijg19/Verse/internal/presenters"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// The maximum number of works the Share picker offers.
//
// The picker is for sharing a work, not for browsing the library -- that is what the Library screen
// is for. A bounded list keeps the page light and keeps the action on its purpose.
const shareWorkLimit = 100

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

	renderSurface(w, r, "share", templ.Share(options))
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

	baseURL := strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/")
	address := ""
	if state.Published {
		address = baseURL + "/w/" + poem.ID + "/"
	}

	published := ""
	if state.PublishedAt != nil {
		published = state.PublishedAt.UTC().Format("2006-01-02")
	}

	body := presenters.BodyWithoutTitle(poem.Content)
	out := struct {
		Title       string `json:"title"`
		Body        string `json:"body"`
		Address     string `json:"address"`
		Published   string `json:"published"`
		IsPublished bool   `json:"isPublished"`
	}{
		Title:       presenters.WorkTitle(poem.Content, presenters.TitleWidthWide),
		Body:        body,
		Address:     address,
		Published:   published,
		IsPublished: state.Published,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		// The status is already committed by the first write, so a failure here can only be
		// reported in the log. The id is left out because it came from the request path and this
		// package has no sanitiser for it; the error is enough to find the failure. The author
		// gets no copied text rather than the wrong text, which is the safe direction.
		log.Printf("write the share data: %v", err)
	}
}
