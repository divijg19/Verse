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
	BaseURL     string
}

func (o Options) normalized() (Options, error) {
	if strings.TrimSpace(o.Title) == "" {
		o.Title = DefaultTitle
	}
	if strings.TrimSpace(o.Description) == "" {
		o.Description = DefaultDescription
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

	return o, nil
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
