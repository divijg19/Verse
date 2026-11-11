package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/divijg19/Verse/internal/push"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// PublishPoemHandler publishes a work, or takes it back off the public site.
//
// The two are one route with a flag rather than two routes, because they are one decision: a work
// is either on the public site or it is not, and the author is toggling that state.
//
// The response is the work's own screen, re-rendered, so the state the author sees afterwards is
// the state that was stored. That matters here in a way it does not elsewhere: the difference
// between "published" and "not published" is the difference between a reader seeing a work and not
// seeing it, and the author should not have to reload to find out which happened.
func PublishPoemHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requireFormWorkID(w, r)
	if !ok {
		return
	}

	publish, ok := requirePublishFlag(w, r)
	if !ok {
		return
	}

	if publish {
		if err := services.PublishPoem(r.Context(), id); err != nil {
			writePublishError(w, r, id, err)
			return
		}
	} else {
		if err := services.UnpublishPoem(r.Context(), id); err != nil {
			writePublishError(w, r, id, err)
			return
		}
	}

	// The publication has happened. The public site is a separate artifact built from a separate
	// store, and putting it there is a separate act -- so it is requested here and its failure is
	// logged rather than answered.
	//
	// The failure is not the author's mistake and must not look like one. The work is published
	// either way; what failed is the step that tells the world about it, and the scheduled deploy
	// will do that within fifteen minutes whether this request succeeds or not. So the author is
	// told the truth -- "Published" -- and the log says what did not happen.
	requestPublicSite(r.Context())

	state, err := services.PublicationState(r.Context(), id)
	if err != nil {
		writePublishError(w, r, id, err)
		return
	}

	poem, err := services.GetPoem(r.Context(), id)
	if err != nil {
		writePoemFetchError(w, r, id, err)
		return
	}

	view := toPoemView(poem)
	view.Published = state.Published
	view.PublishedAt = state.PublishedAt

	renderSurface(w, r, "library", templ.PoemViewContent(r.Context(), view))
}

// requestPublicSite asks for the public site to be rebuilt from the published set.
//
// It is deliberately not able to fail the request. The work is already published -- the author's
// decision has been recorded and is not in doubt -- and the site is a derived artifact that the
// scheduled deploy rebuilds whether or not this succeeds. A failure here is a delay, not a
// mistake, and answering the author with an error page for a delay would be a lie.
//
// A deployment that has not been configured is not a failure either. The authoring service works
// without one, and the site is then published by hand or by the schedule.
func requestPublicSite(ctx context.Context) {
	cfg, err := push.ConfigFromEnv(push.WriteTokenVar)
	if err != nil {
		log.Printf("the public site was not asked for: %v", err)
		return
	}
	if err := cfg.Configured(); err != nil {
		log.Printf("the public site was not asked for: %v", err)
		return
	}

	if err := push.PublishSite(ctx, cfg); err != nil {
		log.Printf("the public site was not asked for: %v", err)
		return
	}

	log.Printf("the public site was asked for")
}

// requirePublishFlag reads the publish flag from the form.
//
// The flag is "1" or "0" rather than two form fields, because a form with a field that is present
// when it means one thing and absent when it means another is a form that has to be read in two
// different ways depending on what the browser sent.
func requirePublishFlag(w http.ResponseWriter, r *http.Request) (bool, bool) {
	switch r.FormValue("publish") {
	case "1":
		return true, true
	case "0":
		return false, true
	default:
		http.Error(w, "missing or malformed publish flag", http.StatusBadRequest)
		return false, false
	}
}

// writePublishError answers a publication that did not happen.
//
// The two failures the author can cause are told apart. A work in the recycle bin cannot be
// published, and the author needs to know that is why rather than being told the work was not
// found: restoring it is a different action from correcting a link.
func writePublishError(w http.ResponseWriter, r *http.Request, id string, err error) {
	if errors.Is(err, services.ErrNotFound) {
		writeMissingWork(w, r, id, "failed to change the publication state", err)
		return
	}

	if errors.Is(err, services.ErrNotPublishable) {
		http.Error(w, "this work is in the recycle bin, so it cannot be published", http.StatusConflict)
		return
	}

	fail500(w, r, "failed to change the publication state", fmt.Errorf("change the publication state for %s: %w", id, err))
}
