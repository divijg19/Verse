package publish

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

// requiredOutputs are the files whose absence would make the deployed site obviously incomplete.
// Work pages are not listed because the set is data-dependent; Render constructs one per work.
var requiredOutputs = []string{
	"index.html",
	"archive/index.html",
	"404.html",
	"search.json",
	"sitemap.xml",
	"feed.xml",
	"robots.txt",
}

// forbiddenSecretSubstrings are values that must never reach a public file. They are scanned as raw
// bytes because a leaked credential name is a defect wherever it appears, including in a title.
var forbiddenSecretSubstrings = []string{
	"npg_",
	"onrender",
	"verse_authorization",
	"verse_auth_secret",
	"database_url",
	"migration_database_url",
}

// forbiddenAuthoringPaths are checked only where they can act as a route: href, src, and the URL
// elements of the sitemap and feed. Scanning prose for these would reject a poem that happens to
// mention the word "library", which is not a leak.
var forbiddenAuthoringPaths = []string{
	"/login",
	"/logout",
	"/library",
	"/poem/",
	"/static/js/",
	"/static/css/",
}

// Validate is the build gate that runs after rendering and before anything reaches the filesystem.
//
// It answers four questions the renderer cannot answer by construction:
//   - are the files a deployed site needs present?
//   - is any authoring route, asset, or secret name present?
//   - does every internal link resolve to a file in this build?
//   - does the public HTML contain an executable script element or an event-handler attribute?
func Validate(files map[string][]byte) error {
	for _, required := range requiredOutputs {
		if _, ok := files[required]; !ok {
			return fmt.Errorf("the build is missing required output %q", required)
		}
	}

	hasStylesheet := false
	for name := range files {
		if strings.HasPrefix(name, "assets/") && strings.HasSuffix(name, ".css") {
			hasStylesheet = true
			break
		}
	}
	if !hasStylesheet {
		return fmt.Errorf("the build contains no public stylesheet under assets/")
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		body := files[name]
		if err := scanForSecrets(name, body); err != nil {
			return err
		}

		switch {
		case strings.HasSuffix(name, ".html"):
			if err := scanHTML(name, body); err != nil {
				return err
			}
			if err := validateLinks(name, body, files); err != nil {
				return err
			}
		case strings.HasSuffix(name, ".json"):
			if err := scanJSON(name, body); err != nil {
				return err
			}
		case strings.HasSuffix(name, ".xml"):
			if err := scanXML(name, body); err != nil {
				return err
			}
		}
	}

	return nil
}

func scanForSecrets(name string, body []byte) error {
	lower := strings.ToLower(string(body))
	for _, needle := range forbiddenSecretSubstrings {
		if strings.Contains(lower, needle) {
			return fmt.Errorf("%s contains %q, which must never reach a public file", name, needle)
		}
	}
	return nil
}

// scanHTML inspects structure, not text. A poem may contain any sequence of characters; what it
// cannot do is create an attribute, a script element, or a link, because templ escapes text nodes.
func scanHTML(name string, body []byte) error {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}

	var walk func(*html.Node) error
	walk = func(node *html.Node) error {
		if node.Type == html.ElementNode {
			if node.Data == "script" {
				return fmt.Errorf("%s contains a script element; the public site ships no JavaScript", name)
			}

			for _, attr := range node.Attr {
				key := strings.ToLower(attr.Key)
				if strings.HasPrefix(key, "hx-") || strings.HasPrefix(key, "hx:") {
					return fmt.Errorf("%s contains the htmx attribute %q", name, attr.Key)
				}
				if len(key) > 2 && strings.HasPrefix(key, "on") {
					return fmt.Errorf("%s contains the event-handler attribute %q", name, attr.Key)
				}
				if key == "href" || key == "src" {
					if err := checkPublicURL(name, attr.Val); err != nil {
						return err
					}
				}
			}
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}

	return walk(doc)
}

func scanJSON(name string, body []byte) error {
	var index struct {
		Entries []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(body, &index); err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}

	for _, entry := range index.Entries {
		if !strings.HasPrefix(entry.Path, "/w/") || !strings.HasSuffix(entry.Path, "/") {
			return fmt.Errorf("%s contains the unexpected public path %q", name, entry.Path)
		}
		if err := checkPublicURL(name, entry.Path); err != nil {
			return err
		}
	}
	return nil
}

func scanXML(name string, body []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}

		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}

		switch start.Name.Local {
		case "loc", "link", "guid":
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return fmt.Errorf("read %s from %s: %w", start.Name.Local, name, err)
			}
			if err := checkPublicURL(name, value); err != nil {
				return err
			}
		}
	}
}

func checkPublicURL(name, value string) error {
	lower := strings.ToLower(value)

	for _, forbidden := range forbiddenAuthoringPaths {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("%s links to the authoring path %q", name, value)
		}
	}
	if strings.Contains(lower, "onrender") {
		return fmt.Errorf("%s links to the authoring host in %q", name, value)
	}

	return nil
}

func validateLinks(name string, body []byte, files map[string][]byte) error {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}

	var walk func(*html.Node) error
	walk = func(node *html.Node) error {
		if node.Type == html.ElementNode {
			for _, attr := range node.Attr {
				if attr.Key != "href" && attr.Key != "src" {
					continue
				}
				ref := strings.TrimSpace(attr.Val)
				if skipLink(ref) {
					continue
				}
				target, ok := resolveLink(name, ref)
				if !ok {
					continue
				}
				if _, exists := files[target]; !exists {
					return fmt.Errorf("%s links to %q, which resolves to %q and is not in the build",
						name, ref, target)
				}
			}
		}

		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}

	return walk(doc)
}

func skipLink(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, "#") {
		return true
	}

	lower := strings.ToLower(ref)
	for _, prefix := range []string{
		"http://", "https://", "//", "mailto:", "tel:", "javascript:", "data:",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	return false
}

// resolveLink maps a reference in one output file to the output map key it must name.
//
// The public site uses rooted paths with trailing slashes, and every directory route is backed by an
// index.html. Resolving rather than string-matching is what makes `/archive/` and
// `/archive/index.html` the same answer, so a template can use the readable form.
func resolveLink(from, ref string) (string, bool) {
	if i := strings.IndexAny(ref, "?#"); i >= 0 {
		ref = ref[:i]
	}
	if ref == "" {
		return "", false
	}

	var target string
	if strings.HasPrefix(ref, "/") {
		target = strings.TrimPrefix(ref, "/")
	} else {
		target = path.Join(path.Dir(from), ref)
	}

	trailingSlash := strings.HasSuffix(target, "/")
	target = path.Clean(target)
	if target == "." {
		target = ""
	}
	if trailingSlash || target == "" || path.Ext(target) == "" {
		target = path.Join(target, "index.html")
	}

	return target, true
}
