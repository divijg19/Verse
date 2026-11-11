package publish

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/divijg19/Verse/internal/export"
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

	// Digest identifies the published set the site is built from.
	//
	// It is written into every page, so the deployed site is the record of what was last
	// deployed and a build can compare against it without keeping a list of its own.
	Digest string

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

// source is one work, as the publisher sees it, before it becomes a public work.
//
// Both the database and the export file are read into sources, and one function turns sources into
// public works. That is the point of the type: the two paths cannot disagree about a title, a path
// or an excerpt, because there is only one place that decides them.
type source struct {
	// ID is the work's stable identifier.
	ID string

	// Content is the work, verbatim.
	Content string

	// PublishedAt is when it was published. It is never zero: a source is a published work.
	//
	// There is no creation instant here, though the database has one and the export carries it.
	// The public site does not show it: a reader meets a work when it was published, and the
	// archive is ordered by that. The field was here because the export has it, which is the
	// wrong reason to carry a value.
	PublishedAt time.Time
}

// publicWorks turns published sources into the public view, in the order a reader met them in.
//
// This is the one place a work becomes a page. The database path and the export-file path both come
// through here, so a build from a file and a build from the database produce the same bytes -- and
// TestTheTwoPathsRenderIdenticalBytes is what holds them to it.
//
// The ordering happens here rather than in each path, and by the shared rule. It was written out
// three times: once per path and once for the front page, and the two path copies were held
// together by the byte-identity test while the third -- which decides what the front page shows --
// was covered by nothing. There is now one comparator, and it is the one all three use.
func publicWorks(sources []source) []publictempl.PublicWork {
	works := make([]publictempl.PublicWork, 0, len(sources))
	for _, s := range sources {
		title := presenters.FirstNonEmptyLine(s.Content)
		if title == "" {
			title = "Untitled"
		}

		works = append(works, publictempl.PublicWork{
			ID:          s.ID,
			Path:        "/w/" + url.PathEscape(s.ID) + "/",
			Title:       title,
			Content:     s.Content,
			Excerpt:     excerpt(s.Content),
			PublishedAt: s.PublishedAt.UTC(),
		})
	}

	slices.SortFunc(works, publictempl.PublishedOrder)

	return works
}

// excerpt is the plain-text summary a work contributes to the feed, the search index and the meta
// description.
//
// It is drawn from the body rather than from the whole work. The title is already the heading on
// the page and the title of the document, so an excerpt that began with it repeated it in every
// summary -- and since every work's title is its first line, the old excerpt was the title followed
// by the whole poem run together on one line.
//
// The cut is on a word boundary, because cutting mid-word leaves a reader with a fragment and an
// ellipsis, and the fragment says nothing.
func excerpt(content string) string {
	body := presenters.BodyWithoutTitle(content)
	return presenters.TruncateAtWord(presenters.FlattenContent(body), ExcerptRunes)
}

// Load reads every published work and maps it to the public view.
//
// The completeness check is the same one the listing makes: a list and a count read by separate
// queries must agree. A public archive built from a truncated list is not a smaller archive; it is a
// site that looks complete and is missing work.
func Load(ctx context.Context) ([]publictempl.PublicWork, string, error) {
	sources, err := loadSources(ctx, services.ListPublishedPoems, services.CountPublishedPoems)
	if err != nil {
		return nil, "", err
	}
	return publicWorks(sources), digest(sources), nil
}

// digest computes the published set's digest.
//
// The export computes the same digest over the same three facts -- an identifier, a publication
// instant and a body -- and one implementation of it is worth a conversion rather than a second
// algorithm that could disagree with the first.
//
// The digest is what the site records, so that a build can compare the published set it is about to
// deploy with the one the live site was built from.
func digest(sources []source) string {
	poems := make([]export.Poem, 0, len(sources))
	for _, s := range sources {
		publishedAt := s.PublishedAt
		poems = append(poems, export.Poem{
			ID:          s.ID,
			Content:     s.Content,
			PublishedAt: &publishedAt,
		})
	}
	return export.PublishedDigest(poems)
}

// load reads the published set and maps it to the public view.
func load(ctx context.Context, read reader, count countFn) ([]publictempl.PublicWork, error) {
	sources, err := loadSources(ctx, read, count)
	if err != nil {
		return nil, err
	}
	return publicWorks(sources), nil
}

// loadSources reads the published set, without mapping it.
//
// The mapping is a separate step so that the database path and the export-file path can share it.
func loadSources(ctx context.Context, read reader, count countFn) ([]source, error) {
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

	sources := make([]source, 0, len(poems))
	seen := make(map[string]struct{}, len(poems))
	for _, poem := range poems {
		id := strings.TrimSpace(poem.ID)
		if id == "" {
			return nil, errors.New("a published work has an empty id")
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

		sources = append(sources, source{
			ID:          id,
			Content:     poem.Content,
			PublishedAt: poem.PublishedAt.UTC(),
		})
	}

	// The query already orders by published_at DESC, id DESC, so the rows arrive in the right
	// order. The order is applied again -- by publicWorks, which both paths go through -- because
	// the reader is injectable here and a test may supply one that does not sort.
	return sources, nil
}
