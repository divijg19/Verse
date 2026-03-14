package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/divijg19/Verse/internal/models"
	"github.com/divijg19/Verse/internal/services"
	"github.com/divijg19/Verse/templ"
)

// LibraryHandler renders the Library surface and supports HTMX partial responses.
func LibraryHandler(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	groups, err := fetchGroupedPoems(r, query)
	if err != nil {
		http.Error(w, "failed to load library", http.StatusInternalServerError)
		return
	}

	renderSurface(w, r, "library", templ.Library(query, groups))
}

// PoemsHandler renders grouped poem rows for dynamic HTMX search updates.
func PoemsHandler(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	groups, err := fetchGroupedPoems(r, query)
	if err != nil {
		http.Error(w, "failed to load poems", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templ.LibraryResults(query, groups).Render(r.Context(), w); err != nil {
		http.Error(w, "failed to render poems", http.StatusInternalServerError)
		return
	}
}

func fetchGroupedPoems(r *http.Request, query string) ([]templ.PoemGroup, error) {
	limit := parseIntDefault(r.URL.Query().Get("limit"), 100)
	offset := parseIntDefault(r.URL.Query().Get("offset"), 0)
	ctx := r.Context()

	var poems []models.Poem
	var err error
	if query != "" {
		poems, err = services.SearchPoems(ctx, query, limit, offset)
	} else {
		poems, err = services.ListPoems(ctx, limit, offset)
	}
	if err != nil {
		return nil, err
	}

	return groupPoems(poems), nil
}

func parseIntDefault(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return fallback
	}
	return v
}

func groupPoems(poems []models.Poem) []templ.PoemGroup {
	if len(poems) == 0 {
		return []templ.PoemGroup{}
	}

	groups := make([]templ.PoemGroup, 0)
	currentLabel := ""
	currentPoems := make([]templ.PoemView, 0)

	for _, poem := range poems {
		label := timelineLabel(poem.CreatedAt)
		if currentLabel == "" {
			currentLabel = label
		}

		if label != currentLabel {
			groups = append(groups, templ.PoemGroup{Label: currentLabel, Poems: currentPoems})
			currentLabel = label
			currentPoems = make([]templ.PoemView, 0)
		}

		currentPoems = append(currentPoems, toPoemView(poem))
	}

	if currentLabel != "" {
		groups = append(groups, templ.PoemGroup{Label: currentLabel, Poems: currentPoems})
	}

	return groups
}

func timelineLabel(t time.Time) string {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := t.UTC().Truncate(24 * time.Hour)

	if day.Equal(today) {
		return "Today"
	}
	if day.Equal(today.AddDate(0, 0, -1)) {
		return "Yesterday"
	}
	if day.Year() == today.Year() {
		return day.Format("Jan 2")
	}
	return day.Format("Jan 2, 2006")
}

func toPoemView(poem models.Poem) templ.PoemView {
	title := firstLine(poem.Content)
	if title == "" {
		title = "Untitled"
	}

	flat := strings.Join(strings.Fields(strings.ReplaceAll(poem.Content, "\n", " ")), " ")

	return templ.PoemView{
		ID:        poem.ID,
		Content:   poem.Content,
		CreatedAt: poem.CreatedAt,
		Title:     truncateRunes(title, 80),
		Snippet:   truncateRunes(flat, 120),
	}
}

func firstLine(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
