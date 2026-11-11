package publish

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/divijg19/Verse/internal/presenters"
	"github.com/divijg19/Verse/internal/services"
	publictempl "github.com/divijg19/Verse/templ"
)

const (
	// DefaultTitle and DefaultDescription are the neutral site identity until a domain and a public
	// name are chosen. They are configuration, not content: the build never reads them from the
	// database.
	DefaultTitle       = "Verse"
	DefaultDescription = "A selection of published work."

	// DefaultTagline is the front screen's line.
	//
	// It is the site's positioning rather than a summary: the description summarizes what a reader
	// will find, and the tagline says what the site is. The two are separate because a sentence
	// that does both does neither.
	DefaultTagline = "Poems, and the literary devices they are built from."

	// DefaultBaseURL is a reserved placeholder. The public surface is noindex until the domain
	// decision lands, so a sitemap and feed must exist but must not claim a real host. Set
	// PUBLIC_BASE_URL (or -base-url) when the site has an address.
	DefaultBaseURL = "https://verse.example"

	// ExcerptRunes is the search-index summary length. D12 keeps the index trimmed: a title, a date,
	// and roughly one sentence, never the full body.
	ExcerptRunes = 200
)

// Options configure a public build. They are data, not environment variables, so a test can produce
// the same output without a process environment.
type Options struct {
	Title       string
	Description string
	Tagline     string
	BaseURL     string

	// Links are the destinations outside this site, shown in the brand pill's menu.
	//
	// They are configuration rather than content for the same reason the title is: the public build
	// never reads them from the database, so a build is a pure function of its published set and its
	// options. An empty list is a valid configuration and produces a brand pill with no menu.
	Links []publictempl.PublicLink
}

func (o Options) normalized() (Options, error) {
	if strings.TrimSpace(o.Title) == "" {
		o.Title = DefaultTitle
	}
	if strings.TrimSpace(o.Description) == "" {
		o.Description = DefaultDescription
	}
	if strings.TrimSpace(o.Tagline) == "" {
		o.Tagline = DefaultTagline
	}

	base := strings.TrimSpace(o.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")

	parsed, err := url.Parse(base)
	if err != nil {
		return Options{}, fmt.Errorf("parse base URL %q: %w", base, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Options{}, fmt.Errorf("base URL %q must use http or https", base)
	}
	if parsed.Host == "" {
		return Options{}, fmt.Errorf("base URL %q has no host", base)
	}
	o.BaseURL = base

	links, err := normalizeLinks(o.Links)
	if err != nil {
		return Options{}, err
	}
	o.Links = links

	return o, nil
}

// normalizeLinks drops empty entries and refuses one that could not work.
//
// A link with no label renders an empty menu entry, and a link with no URL renders an entry that
// goes nowhere. Both are mistakes in the configuration rather than states worth shipping, so the
// build stops and says which one it was.
func normalizeLinks(links []publictempl.PublicLink) ([]publictempl.PublicLink, error) {
	if len(links) == 0 {
		return nil, nil
	}

	cleaned := make([]publictempl.PublicLink, 0, len(links))
	for _, link := range links {
		label := strings.TrimSpace(link.Label)
		address := strings.TrimSpace(link.URL)
		if label == "" && address == "" {
			continue
		}
		if label == "" {
			return nil, fmt.Errorf("a portfolio link has a URL but no label: %q", address)
		}
		if address == "" {
			return nil, fmt.Errorf("portfolio link %q has no URL", label)
		}
		if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
			return nil, fmt.Errorf("portfolio link %q must point at an http or https address, not %q",
				label, address)
		}

		cleaned = append(cleaned, publictempl.PublicLink{
			Label: label,
			URL:   address,
			Note:  strings.TrimSpace(link.Note),
		})
	}

	return cleaned, nil
}

// Load reads every published work and maps it to the public view.
//
// The completeness check is the same one the dry-run makes: a list and a count read by separate
// queries must agree. A public archive built from a truncated list is not a smaller archive; it is a
// site that looks complete and is missing work.
func Load(ctx context.Context) ([]publictempl.PublicWork, error) {
	return load(ctx, services.ListPublishedPoems, services.CountPublishedPoems)
}

func load(ctx context.Context, read reader, count countFn) ([]publictempl.PublicWork, error) {
	poems, err := read(ctx)
	if err != nil {
		return nil, fmt.Errorf("read published works: %w", err)
	}

	counted := len(poems)
	if count != nil {
		if counted, err = count(ctx); err != nil {
			return nil, fmt.Errorf("count published works: %w", err)
		}
	}
	if counted != len(poems) {
		return nil, fmt.Errorf(
			"published set is incomplete: the list returned %d works but the count query reports %d; "+
				"refusing to build an archive that looks complete and is not",
			len(poems), counted)
	}

	works := make([]publictempl.PublicWork, 0, len(poems))
	seen := make(map[string]struct{}, len(poems))
	for _, poem := range poems {
		id := strings.TrimSpace(poem.ID)
		if id == "" {
			return nil, fmt.Errorf("a published work has an empty id")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("published work %q appears twice; paths would collide", id)
		}
		seen[id] = struct{}{}

		if poem.PublishedAt == nil {
			return nil, fmt.Errorf(
				"published work %q has a nil published_at; the publisher's filter should make this "+
					"impossible, so refusing to guess a date", id)
		}

		title := presenters.FirstNonEmptyLine(poem.Content)
		if title == "" {
			title = "Untitled"
		}

		works = append(works, publictempl.PublicWork{
			ID:          id,
			Path:        "/w/" + url.PathEscape(id) + "/",
			Title:       title,
			Content:     poem.Content,
			Excerpt:     presenters.TruncateRunes(presenters.FlattenContent(poem.Content), ExcerptRunes),
			PublishedAt: poem.PublishedAt.UTC(),
		})
	}

	// The query already orders by published_at DESC, id DESC. Sorting again makes the output
	// deterministic even when the reader is injected, and the id tiebreak makes the order total.
	sort.Slice(works, func(i, j int) bool {
		if !works[i].PublishedAt.Equal(works[j].PublishedAt) {
			return works[i].PublishedAt.After(works[j].PublishedAt)
		}
		return works[i].ID > works[j].ID
	})

	return works, nil
}
