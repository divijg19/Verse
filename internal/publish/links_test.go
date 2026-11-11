package publish_test

import (
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/publish"
	publictempl "github.com/divijg19/Verse/templ"
)

func TestParseLinksReadsTheCommandLineForm(t *testing.T) {
	links, err := publish.ParseLinks("Studio=https://studio.example,Notes=https://notes.example/page")
	if err != nil {
		t.Fatalf("ParseLinks: %v", err)
	}

	if len(links) != 2 {
		t.Fatalf("parsed %d links, want 2", len(links))
	}
	if links[0].Label != "Studio" || links[0].URL != "https://studio.example" {
		t.Errorf("the first link is %+v", links[0])
	}
	if links[1].Label != "Notes" || links[1].URL != "https://notes.example/page" {
		t.Errorf("the second link is %+v", links[1])
	}
}

func TestParseLinksTrimsAndSkipsEmptyEntries(t *testing.T) {
	links, err := publish.ParseLinks("  Studio = https://studio.example , , Notes=https://notes.example ")
	if err != nil {
		t.Fatalf("ParseLinks: %v", err)
	}

	if len(links) != 2 {
		t.Fatalf("parsed %d links, want 2", len(links))
	}
	if links[0].Label != "Studio" || links[0].URL != "https://studio.example" {
		t.Errorf("the first link is %+v", links[0])
	}
}

func TestParseLinksAcceptsAnEmptyList(t *testing.T) {
	links, err := publish.ParseLinks("")
	if err != nil {
		t.Fatalf("ParseLinks: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("parsed %d links from an empty spec, want none", len(links))
	}
}

// TestParseLinksRefusesBrokenEntries is the reason this is a parser.
//
// A build that carries a link with no label or no address produces an empty menu entry or an entry
// that goes nowhere. Both are mistakes in the configuration, and the build is the right place to
// find them.
func TestParseLinksRefusesBrokenEntries(t *testing.T) {
	for _, spec := range []string{
		"Studio",
		"=https://studio.example",
		"Studio=",
	} {
		if _, err := publish.ParseLinks(spec); err == nil {
			t.Errorf("ParseLinks(%q) was accepted", spec)
		}
	}
}

// TestBuildRefusesLinksThatAreNotAddresses checks the other half of the rule.
//
// The menu points outward, so a link that is not an address would render a menu entry that goes
// nowhere. The build refuses it.
func TestBuildRefusesLinksThatAreNotAddresses(t *testing.T) {
	_, err := publish.Render(sampleWorks(), publish.Options{
		Title:   "Verse",
		BaseURL: "https://verse.test",
		Links: []publictempl.PublicLink{
			{Label: "Studio", URL: "not-a-url"},
		},
	})
	if err == nil {
		t.Fatal("the build accepted a portfolio link that is not an address")
	}
	if !strings.Contains(err.Error(), "http") {
		t.Errorf("the refusal does not say what is wrong: %v", err)
	}
}

// TestBuildRefusesAPortfolioLinkWithNoLabel covers the other half.
func TestBuildRefusesAPortfolioLinkWithNoLabel(t *testing.T) {
	_, err := publish.Render(sampleWorks(), publish.Options{
		Title:   "Verse",
		BaseURL: "https://verse.test",
		Links:   []publictempl.PublicLink{{URL: "https://studio.example"}},
	})
	if err == nil {
		t.Fatal("the build accepted a portfolio link with no label")
	}
}
