package handlers

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/divijg19/Verse/internal/services"
)

// maxContentRunes bounds a single work's body. Verse holds poems, prose, fragments, and letters;
// none is a manuscript of a million words, and an unbounded column invites accidental pastes of
// entire documents. Counted in runes so multi-byte characters are not penalized.
const maxContentRunes = 200_000

// validationError describes a rejected request without leaking internals.
type validationError struct {
	message string
	status  int
}

func (e *validationError) Error() string { return e.message }

// validateContent normalises and bounds a submitted work body.
//
// Empty and whitespace-only bodies are rejected: they previously created undeletable-looking rows
// in the library, because a blank work has no title to identify it.
func validateContent(raw string) (string, error) {
	// Normalise line endings so a work pasted from Windows does not carry stray CR into the
	// rendered output, which would defeat the whitespace-pre-wrap rendering the reader view relies on.
	normalised := strings.ReplaceAll(raw, "\r\n", "\n")
	normalised = strings.ReplaceAll(normalised, "\r", "\n")
	trimmed := strings.TrimSpace(normalised)

	if trimmed == "" {
		return "", &validationError{message: "a work needs some text", status: http.StatusBadRequest}
	}

	if count := len([]rune(trimmed)); count > maxContentRunes {
		return "", &validationError{
			message: "that work is longer than this build accepts",
			status:  http.StatusRequestEntityTooLarge,
		}
	}

	return trimmed, nil
}

// writeValidationError renders a rejected request.
func writeValidationError(w http.ResponseWriter, err error) {
	var ve *validationError
	if !errors.As(err, &ve) {
		ve = &validationError{message: "invalid request", status: http.StatusBadRequest}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(ve.status)
	writeHTML(w, []byte(`<span class="text-red-400 italic">`+escapeHTML(ve.message)+`</span>`))
}

// escapeHTML is a minimal guard for the small set of literal responses below. templ handles
// escaping for rendered components; these handlers emit hand-written HTML fragments.
func escapeHTML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

// formWorkID extracts and validates the work identifier from a request form.
//
// The value is attacker-supplied, so it is parsed as a UUID rather than trimmed and passed on.
// That bounds what can reach the database, and it bounds what can reach the log: an unvalidated
// string containing newlines would let a caller forge log lines.
func formWorkID(r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.FormValue("id"))
	if raw == "" {
		return "", false
	}

	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", false
	}

	return parsed.String(), true
}

// SavePoemHandler handles saving a work to the database.
//
// Reached only through the authenticated group, with a valid CSRF token and a body capped by the
// router. It still validates its input, because "authenticated" is not the same as "well-formed".
func SavePoemHandler(w http.ResponseWriter, r *http.Request) {
	content, err := validateContent(r.FormValue("content"))
	if err != nil {
		writeValidationError(w, err)
		return
	}

	if _, err := services.CreatePoem(r.Context(), content); err != nil {
		log.Printf("create poem: %v", err)
		http.Error(w, "failed to save poem", http.StatusInternalServerError)
		return
	}

	writeHTML(w, []byte(`<span class="text-purple-400 italic">Bloom recorded.</span>`))
}

// UpdatePoemHandler updates an existing work's content.
func UpdatePoemHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := formWorkID(r)
	if !ok {
		http.Error(w, "missing or malformed id", http.StatusBadRequest)
		return
	}

	content, err := validateContent(r.FormValue("content"))
	if err != nil {
		writeValidationError(w, err)
		return
	}

	if err := services.UpdatePoem(r.Context(), id, content); err != nil {
		if errors.Is(err, services.ErrNotFound) {
			http.Error(w, "poem not found", http.StatusNotFound)
			return
		}
		// #nosec G706 -- id is not raw request input here. formWorkID parsed it as a UUID and
		// returned the canonical string form, so it cannot contain a newline or any other
		// character that would let a caller forge a log line.
		log.Printf("update poem %s: %v", id, err) // #nosec G706
		http.Error(w, "failed to update poem", http.StatusInternalServerError)
		return
	}

	writeHTML(w, []byte(`<span class="text-purple-400 italic">Bloom updated.</span>`))
}

// DeletePoemHandler performs a soft-delete of a work.
func DeletePoemHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := formWorkID(r)
	if !ok {
		http.Error(w, "missing or malformed id", http.StatusBadRequest)
		return
	}

	if err := services.SoftDeletePoem(r.Context(), id); err != nil {
		if errors.Is(err, services.ErrNotFound) {
			// Previously this reported success for a nonexistent id, because the affected-row count
			// was discarded. A false success on a destructive action is worse than an error.
			http.Error(w, "poem not found", http.StatusNotFound)
			return
		}
		// #nosec G706 -- validated UUID, as above.
		log.Printf("soft delete poem %s: %v", id, err) // #nosec G706
		http.Error(w, "failed to delete poem", http.StatusInternalServerError)
		return
	}

	if isHXRequest(r) {
		w.Header().Set("HX-Redirect", "/library")
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Redirect(w, r, "/library", http.StatusSeeOther)
}
