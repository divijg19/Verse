package publish

import (
	"encoding/xml"
	"strings"
	"time"

	publictempl "github.com/divijg19/Verse/templ"
)

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

func sitemapXML(works []publictempl.PublicWork, baseURL string) ([]byte, error) {
	urls := []sitemapURL{
		{Loc: joinBaseURL(baseURL, "/")},
		{Loc: joinBaseURL(baseURL, "/archive/")},
	}
	for _, work := range works {
		urls = append(urls, sitemapURL{
			Loc:     joinBaseURL(baseURL, work.Path),
			LastMod: work.ISODate(),
		})
	}

	body, err := xml.MarshalIndent(sitemapURLSet{
		XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  urls,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Language    string    `xml:"language"`
	Items       []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

func feedXML(works []publictempl.PublicWork, baseURL, title, description string) ([]byte, error) {
	items := make([]rssItem, 0, len(works))
	for _, work := range works {
		link := joinBaseURL(baseURL, work.Path)
		items = append(items, rssItem{
			Title:       work.Title,
			Link:        link,
			GUID:        link,
			PubDate:     work.PublishedAt.UTC().Format(time.RFC1123Z),
			Description: work.Excerpt,
		})
	}

	body, err := xml.MarshalIndent(rssFeed{
		Version: "2.0",
		Channel: rssChannel{
			Title:       title,
			Link:        joinBaseURL(baseURL, "/"),
			Description: description,
			Language:    "en",
			Items:       items,
		},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

// robotsTXT keeps the public surface out of search engines until the domain decision (D7) lands and
// the site is deliberately announced. It is one line of policy, not a placeholder for one.
func robotsTXT() []byte {
	return []byte("User-agent: *\nDisallow: /\n")
}

// joinBaseURL is the one place a path is appended to the configured base, so a trailing slash cannot
// produce a double slash or a missing one.
func joinBaseURL(baseURL, path string) string {
	return strings.TrimRight(baseURL, "/") + path
}
