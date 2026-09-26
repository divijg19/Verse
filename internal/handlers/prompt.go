package handlers

import (
	"html"
	"net/http"

	"github.com/divijg19/Verse/internal/services"
)

// PromptHandler returns a conceptual prompt as an HTML fragment suitable for HTMX.
func PromptHandler(w http.ResponseWriter, r *http.Request) {
	p := services.RandomPrompt()
	writeHTML(w, []byte(`<p class="text-purple-400 italic">`+html.EscapeString(p)+`</p>`))
}
