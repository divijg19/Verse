package publish

import (
	"encoding/json"

	publictempl "github.com/divijg19/Verse/templ"
)

// searchIndexVersion is written into search.json so a future consumer can reject a shape it does not
// understand rather than silently misreading it.
const searchIndexVersion = 1

type searchEntry struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Title     string `json:"title"`
	Published string `json:"published"`
	Excerpt   string `json:"excerpt"`
}

type searchIndex struct {
	Version int           `json:"version"`
	Entries []searchEntry `json:"entries"`
}

// searchJSON is the trimmed client-side search index.
//
// D12's default is deliberate: no full body text, no third-party index, no build-time search bundle.
// The archive page is the no-JavaScript fallback and carries every work; this file only makes a
// future enhancement possible without publishing the library twice.
func searchJSON(works []publictempl.PublicWork) ([]byte, error) {
	entries := make([]searchEntry, 0, len(works))
	for _, work := range works {
		entries = append(entries, searchEntry{
			ID:        work.ID,
			Path:      work.Path,
			Title:     work.Title,
			Published: work.ISODate(),
			Excerpt:   work.Excerpt,
		})
	}

	body, err := json.MarshalIndent(searchIndex{Version: searchIndexVersion, Entries: entries}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}
