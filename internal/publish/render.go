package publish

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/a-h/templ"

	publictempl "github.com/divijg19/Verse/templ"
)

// Render turns a published set into a complete map of output paths to bytes.
//
// This function is pure: no filesystem, no clock, no database. That is what makes determinism
// testable and what lets the validation run before anything is written. Paths are always slash-
// separated keys relative to the output root; WriteDir is the only place that knows about a disk.
func Render(works []publictempl.PublicWork, opts Options) (map[string][]byte, error) {
	opts, err := opts.normalized()
	if err != nil {
		return nil, err
	}

	assetPath, assetBytes, err := publicAsset()
	if err != nil {
		return nil, err
	}

	site := publictempl.PublicSite{
		Title:       opts.Title,
		Description: opts.Description,
		Tagline:     opts.Tagline,
		BaseURL:     opts.BaseURL,
		AssetPath:   assetPath,
		Works:       works,
		Links:       opts.Links,
	}

	files := make(map[string][]byte, len(works)+8)
	add := func(name string, component templ.Component) error {
		if _, exists := files[name]; exists {
			return fmt.Errorf("two outputs would be written to %q", name)
		}
		var buf bytes.Buffer
		if err := component.Render(context.Background(), &buf); err != nil {
			return fmt.Errorf("render %s: %w", name, err)
		}
		files[name] = buf.Bytes()
		return nil
	}

	if err := add("index.html", publictempl.PublicIndex(site)); err != nil {
		return nil, err
	}
	if err := add("archive/index.html", publictempl.PublicArchive(site)); err != nil {
		return nil, err
	}
	if err := add("share/index.html", publictempl.PublicShare(site)); err != nil {
		return nil, err
	}
	if err := add("404.html", publictempl.PublicNotFound(site)); err != nil {
		return nil, err
	}

	for _, work := range works {
		name := strings.TrimPrefix(work.Path, "/") + "index.html"
		if err := add(name, publictempl.PublicWorkPage(site, work)); err != nil {
			return nil, err
		}
	}

	files[strings.TrimPrefix(assetPath, "/")] = assetBytes
	files["robots.txt"] = robotsTXT()

	if files["search.json"], err = searchJSON(works); err != nil {
		return nil, fmt.Errorf("build search.json: %w", err)
	}
	if files["sitemap.xml"], err = sitemapXML(works, opts.BaseURL); err != nil {
		return nil, fmt.Errorf("build sitemap.xml: %w", err)
	}
	if files["feed.xml"], err = feedXML(works, opts.BaseURL, opts.Title, opts.Description); err != nil {
		return nil, fmt.Errorf("build feed.xml: %w", err)
	}

	return files, nil
}
