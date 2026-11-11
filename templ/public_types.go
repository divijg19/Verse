package templ

import (
	"sort"
	"strings"
	"time"
)

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

// Body returns the work without its title line.
//
// The title is the work's first non-blank line, so a page that shows the title as a heading and
// the work as a body has to take that line out of the body once. The two together are the work
// verbatim: nothing is reordered, and nothing is dropped except the line the heading is.
//
// The blank lines that led up to the title go with it, so the body starts where the title ended
// rather than opening with a gap the reader has to scroll past.
//
// The line endings are normalized the same way the title is derived, so a work written on Windows
// does not carry a stray carriage return into the body.
func (w PublicWork) Body() string {
	normalized := strings.ReplaceAll(w.Content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		rest := lines[i+1:]
		return strings.TrimLeft(strings.Join(rest, "\n"), "\n")
	}

	// A work with no line in it at all has no title and no body.
	return ""
}

// PublicLink is one destination outside this site.
//
// The reader's brand pill opens a menu of these: the other corners of the author's portfolio
// ecosystem. They are data rather than markup so that filling the menu in is a build flag and not
// a template edit, and so that a build with no external destinations produces no empty menu.
//
// The site never links to these internally. The reader's own navigation lives on the screen, in the
// rails beside the content, and the pill is for leaving.
type PublicLink struct {
	// Label is the text on the menu entry.
	Label string

	// URL is where the entry goes. It is always absolute, and always http or https.
	URL string

	// Note is an optional line of context under the label.
	Note string
}

// PublicPage names one of the reader's screens.
//
// The page is passed to the layout rather than inferred, because the layout's job is to place the
// two navigation pills that flank it, and which pills those are follows from the page.
type PublicPage string

const (
	// PublicPageHome is the front screen: the site's line and the most recent work.
	PublicPageHome PublicPage = "home"

	// PublicPageArchive is the complete chronological index.
	PublicPageArchive PublicPage = "archive"

	// PublicPageShare is the surface for sending a work to somebody else.
	PublicPageShare PublicPage = "share"

	// PublicPageWork is a single published work.
	PublicPageWork PublicPage = "work"

	// PublicPageMissing is the page served for an address that does not exist.
	PublicPageMissing PublicPage = "missing"
)

// PublicRail is one navigation pill beside the content.
//
// The reader's pages form a ring -- home, then the archive, then share, and home again -- so every
// screen has a neighbor on each side and no screen is more than one click from either of the
// others. The pill's arrow points the way its destination lies: left for the page behind, right
// for the page ahead.
//
// The side is not a field, because the renderer already knows it -- it asks for the left rail or
// the right rail, and a rail that knew its own side would be a second place for that to be true.
type PublicRail struct {
	// Label is the text on the pill.
	Label string

	// Path is where the pill goes.
	Path string
}

// publicRails returns the two pills that flank a page.
//
// The three reader-facing screens form a ring, so each one's left rail is the screen before it and
// its right rail is the screen after it. A work and the not-found page are not part of the ring:
// they carry a single onward pill, on the right, pointing forward into the site.
func publicRails(page PublicPage) (left *PublicRail, right *PublicRail) {
	switch page {
	case PublicPageHome:
		return &PublicRail{Label: "Share", Path: "/share/"},
			&PublicRail{Label: "Archive", Path: "/archive/"}
	case PublicPageArchive:
		return &PublicRail{Label: "Home", Path: "/"},
			&PublicRail{Label: "Share", Path: "/share/"}
	case PublicPageShare:
		return &PublicRail{Label: "Archive", Path: "/archive/"},
			&PublicRail{Label: "Home", Path: "/"}
	case PublicPageWork:
		return nil, &PublicRail{Label: "Archive", Path: "/archive/"}
	case PublicPageMissing:
		return nil, &PublicRail{Label: "Home", Path: "/"}
	default:
		return nil, nil
	}
}

// PublicSite is the complete input to the public renderer.
//
// It contains no build timestamp. Output is therefore a pure function of the published set, the
// configuration in this struct, and the static asset bytes -- which is what makes two builds from the
// same input byte-identical.
type PublicSite struct {
	Title       string
	Description string
	Tagline     string
	BaseURL     string
	AssetPath   string
	Works       []PublicWork

	// Links are the destinations outside this site, shown in the brand pill's menu.
	Links []PublicLink
}

// Address returns the absolute address of a path on this site.
//
// The share surface shows this rather than the rooted path, because the rooted path is what the
// site uses for itself and the absolute address is what the reader needs in order to send it to
// somebody else.
func (s PublicSite) Address(path string) string {
	return strings.TrimRight(s.BaseURL, "/") + path
}

// RecentWorks returns the most recently published works, up to a limit.
//
// The works are chosen by publication date rather than taken from the front of the slice, so the
// answer does not depend on the caller having sorted the set first. The publisher does sort it, and
// the tiebreak matches the publisher's own: the same instant falls back to the identifier, so two
// works published together keep a fixed order between builds.
//
// The input slice is not modified.
func (s PublicSite) RecentWorks(limit int) []PublicWork {
	if limit <= 0 || len(s.Works) == 0 {
		return nil
	}

	ordered := make([]PublicWork, len(s.Works))
	copy(ordered, s.Works)

	sort.Slice(ordered, func(i, j int) bool {
		if !ordered[i].PublishedAt.Equal(ordered[j].PublishedAt) {
			return ordered[i].PublishedAt.After(ordered[j].PublishedAt)
		}
		return ordered[i].ID > ordered[j].ID
	})

	if len(ordered) < limit {
		return ordered
	}
	return ordered[:limit]
}
