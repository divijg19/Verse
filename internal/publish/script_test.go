package publish_test

import (
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/publish"
)

// TestThePublicSiteCarriesOneScriptAndNoInlineOne is the rule the site is built around.
//
// The public site ships no third-party requests and no inline script, and for most of its life it
// shipped no script at all. The archive's search box is the one exception, and it is a file this
// build wrote and named.
//
// The exception is worth a test, because "the public site has no script" was a property that was
// true until v0.5.0 and is now true only in a narrower sense: one script, from this origin, named
// by this build, and nothing inline. A rule that quietly widens is a rule nobody is watching.
func TestThePublicSiteCarriesOneScriptAndNoInlineOne(t *testing.T) {
	files, err := publish.Render(sampleWorks(), testOptions())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, name := range []string{"index.html", "archive/index.html", "share/index.html", "404.html"} {
		page, ok := files[name]
		if !ok {
			t.Fatalf("the build has no %q", name)
		}

		body := string(page)

		if got := strings.Count(body, "<script"); got != 1 {
			t.Errorf("%s carries %d script elements, want exactly one", name, got)
		}
		if strings.Contains(body, "<script>") {
			t.Errorf("%s carries an inline script", name)
		}
		if strings.Contains(body, " on") && strings.Contains(body, "onload=") {
			t.Errorf("%s carries an event-handler attribute", name)
		}
	}
}

// TestTheValidatorRefusesAScriptItDidNotWrite is the other half of the rule.
//
// The validator allows the one script the build wrote and refuses everything else: an inline script,
// and a script pointing anywhere but at this build's own bundle. The second is what stops a stray
// third-party script from being added to a page and passing because scripts are allowed now.
func TestTheValidatorRefusesAScriptItDidNotWrite(t *testing.T) {
	base, err := publish.Render(sampleWorks(), testOptions())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	t.Run("an inline script", func(t *testing.T) {
		files := copyFiles(base)
		files["index.html"] = append(files["index.html"], []byte(`<script>alert(1)</script>`)...)
		if err := publish.Validate(files, "https://verse.test"); err == nil {
			t.Fatal("validation accepted an inline script")
		}
	})

	t.Run("a script from somewhere else", func(t *testing.T) {
		files := copyFiles(base)
		files["index.html"] = append(files["index.html"],
			[]byte(`<script src="https://example.com/thing.js"></script>`)...)
		if err := publish.Validate(files, "https://verse.test"); err == nil {
			t.Fatal("validation accepted a script from another origin")
		}
	})
}
