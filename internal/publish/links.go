package publish

import (
	"fmt"
	"strings"

	publictempl "github.com/divijg19/Verse/templ"
)

// ParseLinks reads the portfolio link list from its command-line form.
//
// The format is a comma-separated list of Label=URL pairs:
//
//	Studio=https://studio.example,Notes=https://notes.example
//
// A label may contain spaces; a URL may not contain a comma, because a comma separates one entry
// from the next. That is a limitation of the format rather than of the data, and it is stated here
// because a URL with a comma in it is silently wrong otherwise.
//
// The reason this is a parser and not a flag value carried straight through is that the build has
// to be able to refuse a broken entry. A link with no label, or with a URL that is not an address,
// is a mistake in the configuration, and a build that stops and names it is more use than one that
// ships an empty menu entry.
func ParseLinks(spec string) ([]publictempl.PublicLink, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	parts := strings.Split(spec, ",")
	links := make([]publictempl.PublicLink, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		equals := strings.Index(part, "=")
		if equals < 0 {
			return nil, fmt.Errorf(
				"portfolio link %q is not in Label=URL form; write Studio=https://studio.example", part)
		}

		label := strings.TrimSpace(part[:equals])
		address := strings.TrimSpace(part[equals+1:])
		if label == "" {
			return nil, fmt.Errorf("portfolio link %q has no label", address)
		}
		if address == "" {
			return nil, fmt.Errorf("portfolio link %q has no URL", label)
		}

		links = append(links, publictempl.PublicLink{Label: label, URL: address})
	}

	return links, nil
}
