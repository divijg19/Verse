package handlers

import (
	"errors"
	"fmt"
	"html"
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
	writeHTML(w, []byte(`<span class="text-red-400 italic">`+html.EscapeString(ve.message)+`</span>`))
}

// formWorkID extracts and validates the work identifier from a request form.
func formWorkID(r *http.Request) (string, bool) {
	return parseWorkID(r.FormValue("id"))
}

// requireFormWorkID returns the work id from the "id" form field, or answers 400 and reports false.
//
// The bool-and-early-return shape every caller was already writing by hand, seven times. Folding the
// error response in is safe because the response is identical at every site: same status, same
// message, no leak either way -- a malformed id is the caller's own fault and saying so tells them
// nothing about the database.
func requireFormWorkID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := formWorkID(r)
	if !ok {
		http.Error(w, "missing or malformed id", http.StatusBadRequest)
		return "", false
	}
	return id, true
}

// requirePathWorkID is requireFormWorkID for the {id} route parameter.
func requirePathWorkID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := pathWorkID(r)
	if !ok {
		http.Error(w, "missing or malformed id", http.StatusBadRequest)
		return "", false
	}
	return id, true
}

// parseWorkID validates a work identifier that came from an untrusted source.
//
// The value is attacker-supplied, so it is parsed as a UUID rather than trimmed and passed on. That
// bounds what can reach the database, and it bounds what can reach the log: an unvalidated string
// containing newlines would let a caller forge log lines.
//
// Shared by formWorkID, which reads the "id" form field, and pathWorkID, which reads the {id} route
// parameter. They were two copies of this function, and the two differed in one respect that mattered
// only by accident: formWorkID trimmed the raw value and pathWorkID did not, so a path parameter
// carrying a stray space was rejected while the same value in a form was accepted. Whether a UUID may
// arrive padded is not a question the call site should be deciding.
func parseWorkID(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
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
		fail500(w, r, "failed to save poem", fmt.Errorf("create poem: %w", err))
		return
	}

	writeHTML(w, []byte(`<span class="text-purple-400 italic">Bloom recorded.</span>`))
}

// UpdatePoemHandler updates an existing work's content.
func UpdatePoemHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requireFormWorkID(w, r)
	if !ok {
		return
	}

	content, err := validateContent(r.FormValue("content"))
	if err != nil {
		writeValidationError(w, err)
		return
	}

	if err := services.UpdatePoem(r.Context(), id, content); err != nil {
		writeMissingWork(w, r, id, "failed to update poem", fmt.Errorf("update poem: %w", err))
		return
	}

	writeHTML(w, []byte(`<span class="text-purple-400 italic">Bloom updated.</span>`))
}

// DeletePoemHandler performs a soft-delete of a work.
func DeletePoemHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := requireFormWorkID(w, r)
	if !ok {
		return
	}

	// A false success on a destructive action is worse than an error, so a nonexistent id is a 404.
	// It was previously a success, because the affected-row count was discarded.
	if err := services.SoftDeletePoem(r.Context(), id); err != nil {
		writeMissingWork(w, r, id, "failed to delete poem", fmt.Errorf("soft delete poem: %w", err))
		return
	}

	if isHXRequest(r) {
		w.Header().Set("HX-Redirect", "/library")
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Redirect(w, r, "/library", http.StatusSeeOther)
}
