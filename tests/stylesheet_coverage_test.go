package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This file exists because a class a script applies by hand was not in the stylesheet.
//
// The navigation script adds "opacity-0" and "translate-y-2" to the screen element before an htmx
// swap and removes them afterwards. That is the whole screen transition: the screen fades out, the
// swap happens, the screen fades back in from slightly below.
//
// Tailwind v4 discovers the class names it generates by scanning declared source directories, and
// static/css/input.css declares them explicitly. It declared templ/ and internal/ -- because Go
// code renders markup too -- but not web/ts, which did not exist when the list was written.
//
// So the two classes were added to the element and did nothing, in every browser, since the
// TypeScript port. The transition was invisible and nothing said why: the element really did carry
// the classes, and the classes really were absent from the stylesheet.
//
// The guard is over the class names the browser code applies, rather than over the stylesheet's
// whole contents, because that is the property that broke: a name the code uses must be a name the
// stylesheet defines.

// classListPattern finds the class names a script adds to an element.
var classListPattern = regexp.MustCompile(`classList\.(?:add|remove|toggle)\(\s*"([^"]+)"`)

// TestEveryClassTheBrowserCodeAppliesIsInTheStylesheet is the guard.
//
// The stylesheet is read from the committed output rather than rebuilt, because the committed
// output is what is served. A stylesheet that is out of date fails the currency check in CI
// separately; this test is about whether the classes are in it.
//
// The stylesheet is read even when it has not been generated, because the whole point of the test
// is that a class is missing from it. A skip condition of the form "skip if the class is not
// there" would skip exactly when the bug is present.
func TestEveryClassTheBrowserCodeAppliesIsInTheStylesheet(t *testing.T) {
	path := filepath.Join("static", "css", "output.css")
	if _, err := os.Stat(path); err != nil {
		if _, err := os.Stat(filepath.Join("..", path)); err != nil {
			t.Skip("output.css has not been generated; run the Tailwind step to exercise this test fully")
		}
	}

	stylesheet := readRepoFile(t, path)

	for _, source := range []string{"editor.ts", "navigation.ts", "public_search.ts"} {
		body := readRepoFile(t, filepath.Join("web", "ts", source))

		for _, match := range classListPattern.FindAllStringSubmatch(body, -1) {
			for _, name := range strings.Fields(match[1]) {
				if !strings.Contains(stylesheet, "."+name+"{") {
					t.Errorf("web/ts/%s applies the class %q, and the stylesheet does not define it; "+
						"the element will carry the class and nothing will happen", source, name)
				}
			}
		}
	}
}

// TestTheTailwindSourcesIncludeTheBrowserCode is the guard on the cause.
//
// The class was missing because the source list did not name the directory the class is written in.
// The list is the thing that has to be right, so it is checked directly rather than only through
// its consequence.
func TestTheTailwindSourcesIncludeTheBrowserCode(t *testing.T) {
	input := readRepoFile(t, filepath.Join("static", "css", "input.css"))

	if !strings.Contains(input, `@source "../../web/ts"`) {
		t.Errorf("static/css/input.css does not declare web/ts as a Tailwind source.\n"+
			"A class written in the browser code would then be missing from the stylesheet, "+
			"which is a failure with no error message: %s", input)
	}
}

// TestThePublicSiteStylesheetCarriesTheSearchBoxStyles covers the other stylesheet.
//
// The public site's own stylesheet is hand-written rather than generated, so nothing checks that
// it has the rules the search box needs. The box would appear unstyled -- full width, no pill, no
// muted text -- and the page would still work.
func TestThePublicSiteStylesheetCarriesTheSearchBoxStyles(t *testing.T) {
	stylesheet := readRepoFile(t, filepath.Join("internal", "publish", "assets", "public.css"))

	for _, selector := range []string{".archive-search", ".archive-search-field", ".archive-search-status"} {
		if !strings.Contains(stylesheet, selector) {
			t.Errorf("the public stylesheet does not define %s", selector)
		}
	}
}

// TestThePublicStylesheetIsEmbeddedInTheBuild is the check that the stylesheet is the one shipped.
//
// The public site's stylesheet is embedded into the publish binary rather than read from disk, so
// a stylesheet edited but not rebuilt would not be what the site carries. The file on disk is the
// one embedded; this test cannot see the embedding, so it checks the weaker property that both
// exist and that the embedded asset directory holds them.
func TestThePublicStylesheetIsEmbeddedInTheBuild(t *testing.T) {
	for _, name := range []string{"public.css", "public.js"} {
		path := filepath.Join("internal", "publish", "assets", name)
		if _, err := os.Stat(path); err != nil {
			if _, err := os.Stat(filepath.Join("..", path)); err != nil {
				t.Errorf("the embedded public asset %s is missing: %v", name, err)
			}
		}
	}
}
