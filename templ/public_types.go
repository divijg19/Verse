package templ

import "time"

// PublicWork is one published work as the public site sees it.
//
// It is deliberately its own type rather than models.Poem. The public build is a different surface
// with a different contract: it carries only what a reader may see, and it has no field for a draft,
// a soft-delete instant, or revision history. A build cannot accidentally render a private column it
// was never handed.
type PublicWork struct {
	// ID is the stable identifier from the database.
	ID string

	// Path is the public route, always rooted and always ending in a slash.
	Path string

	// Title is the first non-blank line of the work, or "Untitled" when the work has no such line.
	Title string

	// Content is the work verbatim. It is escaped by templ at render time and is never interpreted
	// as HTML or Markdown.
	Content string

	// Excerpt is a plain-text, bounded summary for the search index and feed.
	Excerpt string

	// PublishedAt is when the author published the work. It is never zero for a published work.
	PublishedAt time.Time
}

// DisplayDate returns the human-readable publication date.
func (w PublicWork) DisplayDate() string {
	return w.PublishedAt.UTC().Format("January 2, 2006")
}

// ISODate returns the machine-readable publication date for a datetime attribute.
func (w PublicWork) ISODate() string {
	return w.PublishedAt.UTC().Format("2006-01-02")
}

// PublicSite is the complete input to the public renderer.
//
// It contains no build timestamp. Output is therefore a pure function of the published set, the
// configuration in this struct, and the static asset bytes -- which is what makes two builds from the
// same input byte-identical.
type PublicSite struct {
	Title       string
	Description string
	BaseURL     string
	AssetPath   string
	Works       []PublicWork
}
