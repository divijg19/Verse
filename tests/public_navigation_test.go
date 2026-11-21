package tests

import (
	"strings"
	"testing"
	"time"

	page "github.com/a-h/templ"
	views "github.com/divijg19/Verse/templ"
)

// TestAuthorNavigationButtonsArePills locks in the shape of the authoring navigation.
//
// The buttons were a column of a rounded rectangle, which reads as a panel. They are now pills:
// one row, the chevron and the label side by side, with a full radius. The assertions are on the
// properties rather than on the class name, because the class name is not what makes a pill.
func TestAuthorNavigationButtonsArePills(t *testing.T) {
	body := renderComponent(t, views.Layout("editor", views.Editor(renderContext())))

	assertContainsAll(t, body,
		`border-radius: 999px;`,
		`flex-direction: row;`,
		`gap: 0.45rem;`,
		`min-height: 2.5rem;`,
		`padding: 0.4rem 1rem;`,
		`transform: translateY(-1px);`,
		`box-shadow: 0 0 0 2px rgb(168 85 247);`,
	)
}

// TestAuthorNavigationArrowsPointTheRightWay is the regression for the arrows.
//
// The chevron is a square with two of its borders drawn, so its corner points south-east and
// turning it costs a rotation. The base was -45 degrees, which points east, so the top button
// pointed at the right-hand rail; and the right button was set to -135 degrees, which points north,
// so it pointed at the top. The two were the wrong way round.
//
// The rotations below are the measured ones: -135 is north, -45 is east, 45 is south, 135 is west.
func TestAuthorNavigationArrowsPointTheRightWay(t *testing.T) {
	body := renderComponent(t, views.Layout("editor", views.Editor(renderContext())))

	assertContainsAll(t, body,
		`.verse-nav-icon {`,
		`transform: rotate(-135deg);`,
		`#nav-left .verse-nav-icon {`,
		`transform: rotate(135deg);`,
		`#nav-right .verse-nav-icon {`,
		`transform: rotate(-45deg);`,
		`#nav-bottom .verse-nav-icon {`,
		`transform: rotate(45deg);`,
	)

	// The base rotation is the top button's, so the top slot must not override it. An override
	// there would mean the arrow and the slot disagree.
	if strings.Contains(body, `#nav-top .verse-nav-icon`) {
		t.Fatalf("the top slot overrides the base rotation; the two must agree")
	}
}

// TestAuthorSignOutHasItsOwnSlotAndAQuietStyle locks in the sign-out's treatment.
//
// The session lasts eight hours and this is the only control that ends it, so it stays. What
// changed is that it has a grid slot of its own instead of an auto-placed position, and that it is
// a ghost pill -- no fill, muted ink -- rather than a button that competes with the navigation.
func TestAuthorSignOutHasItsOwnSlotAndAQuietStyle(t *testing.T) {
	body := renderComponent(t, views.Layout("editor", views.Editor(renderContext())))

	assertContainsAll(t, body,
		`id="nav-signout"`,
		`class="verse-signout-form"`,
		`class="verse-signout-button">Sign out</button>`,
		`grid-area: signout;`,
		`.verse-signout-button {`,
		`border-radius: 999px;`,
		`background: transparent;`,
		`color: rgb(115 115 115);`,
		`letter-spacing: 0.18em;`,
	)

	// The grid has to name the slot, or the area is declared and never filled.
	assertContainsAll(t, body,
		`". nav-top signout"`,
		`"nav-left screen nav-right"`,
		`". nav-bottom ."`,
	)
}

// TestAuthorMobileNavigationIsStyled guards the mobile sheet.
//
// The desktop rails are a grid, and the grid does not fit a phone, so below 1024px the navigation
// moves into a sheet behind a pill toggle. The styles for that sheet were removed at the same time
// as the masthead they used to sit under, which left the toggle unstyled and the sheet invisible.
func TestAuthorMobileNavigationIsStyled(t *testing.T) {
	body := renderComponent(t, views.Layout("editor", views.Editor(renderContext())))

	assertContainsAll(t, body,
		`#mobile-nav {`,
		`.verse-mobile-nav-bar {`,
		`.verse-mobile-nav-bar-inner {`,
		`.verse-mobile-nav-toggle {`,
		`.verse-mobile-nav-sheet {`,
		`.verse-mobile-nav-backdrop {`,
		`.verse-mobile-nav-panel {`,
		`.verse-mobile-nav-link {`,
		`.verse-mobile-nav-actions {`,
		`border-radius: 999px;`,
	)
}

// TestAuthorNavigationRenderedForEverySurface checks that each surface's rails carry the buttons
// the navigation matrix promises, and that each one carries a chevron.
//
// A surface with an empty rail renders a gap where a destination should be, and a button with no
// chevron is a button with no direction.
func TestAuthorNavigationRenderedForEverySurface(t *testing.T) {
	for _, surface := range []string{"dashboard", "editor", "library", "caelum", "share"} {
		t.Run(surface, func(t *testing.T) {
			body := renderNavOOB(t, surface)

			buttons := strings.Count(body, `class="verse-desktop-nav-button"`)
			if buttons == 0 {
				t.Fatalf("the %s surface renders no navigation buttons", surface)
			}

			chevrons := strings.Count(body, `class="verse-nav-icon"`)
			if chevrons != buttons {
				t.Fatalf("the %s surface renders %d buttons and %d chevrons; every button needs one",
					surface, buttons, chevrons)
			}
		})
	}
}

// TestPublicShellNavigatesOnScreen checks that each of the two reader screens carries one pill.
func TestPublicShellNavigatesOnScreen(t *testing.T) {
	for _, page := range []views.PublicPage{
		views.PublicPageHome,
		views.PublicPageArchive,
	} {
		t.Run(string(page), func(t *testing.T) {
			body := renderComponent(t, views.PublicLayout(
				views.PublicSite{Title: "Verse", BaseURL: "https://verse.test"},
				page,
				"Verse",
				"A selection of published work.",
				views.PublicIndexBody(views.PublicSite{}),
			))

			assertContainsAll(t, body,
				`class="public-shell"`,
				`class="rail-pill"`,
				`class="rail-chevron"`,
			)
			if strings.Count(body, `class="rail-pill"`) != 1 {
				t.Errorf("page %s does not carry exactly one rail", page)
			}
		})
	}
}

// TestPublicRailsPointAtEachOther checks the two-screen navigation: Home and Archive point at one
// another, and neither carries a second pill.
func TestPublicRailsPointAtEachOther(t *testing.T) {
	cases := []struct {
		page        views.PublicPage
		left, right string
	}{
		{page: views.PublicPageHome, right: "Archive"},
		{page: views.PublicPageArchive, left: "Home"},
	}

	for _, tc := range cases {
		t.Run(string(tc.page), func(t *testing.T) {
			body := renderComponent(t, views.PublicLayout(
				views.PublicSite{Title: "Verse", BaseURL: "https://verse.test"},
				tc.page,
				"Verse",
				"A selection of published work.",
				views.PublicIndexBody(views.PublicSite{}),
			))

			left := railLabelIn(body, "public-rail--left")
			right := railLabelIn(body, "public-rail--right")
			if left != tc.left {
				t.Errorf("the left rail reads %q, want %q", left, tc.left)
			}
			if right != tc.right {
				t.Errorf("the right rail reads %q, want %q", right, tc.right)
			}
		})
	}
}

// TestPublicBrandPillHoldsExternalSitesOnly checks the pill's contents.
//
// The pill is the only thing on the reader's site that opens a menu, and the menu points outward:
// at the author's other portfolio sites. The site's own pages are in the rails, so putting them in
// the menu too would hide the navigation the reader can already see.
func TestPublicBrandPillHoldsExternalSitesOnly(t *testing.T) {
	site := views.PublicSite{
		Title:   "Verse",
		BaseURL: "https://verse.test",
		Links: []views.PublicLink{
			{Label: "Studio", URL: "https://studio.example/"},
			{Label: "Notes", URL: "https://notes.example/"},
		},
	}

	body := renderComponent(t, views.PublicLayout(
		site,
		views.PublicPageHome,
		"Verse",
		"A selection of published work.",
		views.PublicIndexBody(site),
	))

	assertContainsAll(t, body,
		`<details class="brand">`,
		`<summary class="brand-pill">`,
		`class="brand-menu-item" href="https://studio.example/"`,
		`class="brand-menu-item" href="https://notes.example/"`,
		`>Studio</span>`,
		`>Notes</span>`,
	)

	// The site's own pages are not in the menu.
	for _, internal := range []string{`href="/archive/"`} {
		if menu := between(body, `<div class="brand-menu">`, `</div>`); strings.Contains(menu, internal) {
			t.Errorf("the brand menu links to the site's own page %q; the rails carry those", internal)
		}
	}
}

// TestPublicBrandPillIsAHomeLinkWithoutExternalSites checks the empty case.
//
// A pill that opens a menu with nothing in it is a button that does nothing. With no external
// destinations configured, the pill is a plain link home.
func TestPublicBrandPillIsAHomeLinkWithoutExternalSites(t *testing.T) {
	site := views.PublicSite{Title: "Verse", BaseURL: "https://verse.test"}

	body := renderComponent(t, views.PublicLayout(
		site,
		views.PublicPageHome,
		"Verse",
		"A selection of published work.",
		views.PublicIndexBody(site),
	))

	assertContainsAll(t, body,
		`<a class="brand-pill" href="/">`,
		`<span class="brand-pill-name">Verse</span>`,
	)

	if strings.Contains(body, "brand-menu") {
		t.Error("the brand menu renders with no external sites configured")
	}
	if strings.Contains(body, "<details") {
		t.Error("the brand pill opens a menu with no external sites configured")
	}
}

// TestPublicPagesCarryNoAuthoringClassNames checks that no authoring marker reaches a public page.
func TestPublicPagesCarryNoAuthoringClassNames(t *testing.T) {
	site := views.PublicSite{Title: "Verse", BaseURL: "https://verse.test"}

	for name, component := range map[string]page.Component{
		"index":   views.PublicIndex(site),
		"archive": views.PublicArchive(site),
		"404":     views.PublicNotFound(site),
	} {
		t.Run(name, func(t *testing.T) {
			body := renderComponent(t, component)
			for _, forbidden := range []string{
				"verse-button", "verse-desktop-nav", "verse-panel", "verse-surface",
				"verse-eyebrow", "hx-get", "hx-swap", "hx-target",
			} {
				if strings.Contains(body, forbidden) {
					t.Errorf("the public %s page carries the authoring marker %q", name, forbidden)
				}
			}
		})
	}
}

// TestPublicArchiveOffersTheThreeActions checks the one screen that lists works and offers the three
// actions. The address is not visible; it lives in a data attribute, and the buttons start hidden.
func TestPublicArchiveOffersTheThreeActions(t *testing.T) {
	site := views.PublicSite{
		Title:   "Verse",
		BaseURL: "https://verse.test",
		Works: []views.PublicWork{
			{ID: "a", Path: "/w/a/", Title: "The First Work", PublishedAt: fixedPublicTime(3)},
		},
	}

	body := renderComponent(t, views.PublicArchive(site))

	assertContainsAll(t, body,
		`class="share-item"`,
		`data-share-address="https://verse.test/w/a/"`,
		`class="work-title share-title" href="/w/a/"`,
		`data-share-action="text"`,
		`data-share-action="link"`,
		`data-share-action="card"`,
		`>Copy Text</button>`,
		`>Copy Link</button>`,
		`>Card Export</button>`,
		`<a href="/feed.xml">RSS feed</a>`,
	)
	if strings.Contains(body, `class="share-address"`) || strings.Contains(body, `https://verse.test/w/a/</span>`) {
		t.Error("the Archive exposes a raw work address as page text")
	}
	if strings.Contains(body, "Verse</h1>") {
		t.Error("the Archive publishes a work's body twice")
	}

	// The compiled bundle reads the data attribute and reveals the hidden group after wiring.
	bundle := readRepoFile(t, "../internal/publish/assets/public.js")
	for _, needle := range []string{"data-share-action", "dataset.shareAddress", "hidden = false"} {
		if !strings.Contains(bundle, needle) {
			t.Errorf("the public bundle does not contain %q", needle)
		}
	}
}

// TestPublicHomeShowsATeaserAndTheArchiveShowsEverything checks that the front page stays a teaser
// while the archive is the complete server-rendered index.
func TestPublicHomeShowsATeaserAndTheArchiveShowsEverything(t *testing.T) {
	// The dates decide the teaser: Work Four is newest and Work One oldest.
	works := []views.PublicWork{
		{ID: "a", Path: "/w/a/", Title: "Work One", PublishedAt: fixedPublicTime(1)},
		{ID: "b", Path: "/w/b/", Title: "Work Two", PublishedAt: fixedPublicTime(2)},
		{ID: "c", Path: "/w/c/", Title: "Work Three", PublishedAt: fixedPublicTime(3)},
		{ID: "d", Path: "/w/d/", Title: "Work Four", PublishedAt: fixedPublicTime(4)},
	}
	site := views.PublicSite{Title: "Verse", BaseURL: "https://verse.test", Works: works}

	home := renderComponent(t, views.PublicIndex(site))
	for _, want := range []string{"Work Four", "Work Three", "Work Two"} {
		if !strings.Contains(home, want) {
			t.Errorf("the front page does not show %q", want)
		}
	}
	if strings.Contains(home, "Work One") {
		t.Error("the front page shows more works than its teaser")
	}

	archive := renderComponent(t, views.PublicArchive(site))
	for _, want := range []string{"Work One", "Work Two", "Work Three", "Work Four"} {
		if !strings.Contains(archive, want) {
			t.Errorf("the archive does not show %q", want)
		}
	}
	if strings.Count(archive, `class="share-item"`) != len(works) {
		t.Errorf("the archive rendered %d rows, want %d", strings.Count(archive, `class="share-item"`), len(works))
	}
}

// TestPublicPagesDoNotBlatantlyTitleThemselves checks that no visible heading repeats the page name.
func TestPublicPagesDoNotBlatantlyTitleThemselves(t *testing.T) {
	site := views.PublicSite{Title: "Verse", BaseURL: "https://verse.test"}

	home := renderComponent(t, views.PublicIndex(site))
	if strings.Contains(home, `<h1>Verse</h1>`) {
		t.Error("the front page renders the brand as a heading")
	}
	if !strings.Contains(home, `<h1 class="visually-hidden">Verse</h1>`) {
		t.Error("the front page has no heading at all")
	}

	archive := renderComponent(t, views.PublicArchive(site))
	if strings.Contains(archive, `<h1>Archive</h1>`) {
		t.Error("the archive renders its name as a visible heading")
	}
	if !strings.Contains(archive, `<h1 class="visually-hidden">Archive</h1>`) {
		t.Error("the archive has no heading at all")
	}
}

// TestPublicFrontScreenIsAHero checks the landing screen.
//
// The front screen is the site's only hero, and the line it leads with is the site's positioning
// rather than its name. A reader arriving at the site for the first time learns what it is from
// that line, and the sentence underneath says what they will find in it.
func TestPublicFrontScreenIsAHero(t *testing.T) {
	site := views.PublicSite{
		Title:       "Verse",
		Tagline:     "Poems, and the literary devices they are built from.",
		Description: "A selection of published work.",
		BaseURL:     "https://verse.test",
	}

	body := renderComponent(t, views.PublicIndex(site))

	assertContainsAll(t, body,
		`class="hero"`,
		`class="hero-line">Poems, and the literary devices they are built from.</p>`,
		`class="lede">A selection of published work.</p>`,
	)

	// The name is in the pill, so the hero must not repeat it.
	if strings.Contains(body, "hero-line\">Verse<") {
		t.Error("the hero repeats the site's name")
	}
}

// TestPublicShellHasNoFooter checks that the footer is absent from every reader page.
func TestPublicShellHasNoFooter(t *testing.T) {
	site := views.PublicSite{Title: "Verse", BaseURL: "https://verse.test"}
	for name, component := range map[string]page.Component{
		"index":   views.PublicIndex(site),
		"archive": views.PublicArchive(site),
		"404":     views.PublicNotFound(site),
	} {
		t.Run(name, func(t *testing.T) {
			body := renderComponent(t, component)
			for _, footer := range []string{"public-footer", "<footer"} {
				if strings.Contains(body, footer) {
					t.Errorf("the public %s page still renders a footer", name)
				}
			}
		})
	}
}

// TestPublicRailArrowsPointOutward checks the arrows' order within their pills.
//
// The left rail's arrow leads its label and points back at where the reader came from; the right
// rail's trails its label and points forward at what comes next. An arrow that led its label on
// the right would point into the word it is supposed to be pointing away from.
func TestPublicRailArrowsPointOutward(t *testing.T) {
	site := views.PublicSite{Title: "Verse", BaseURL: "https://verse.test"}

	renderComponent(t, views.PublicLayout(
		site,
		views.PublicPageHome,
		"Verse",
		"A selection of published work.",
		views.PublicIndexBody(site),
	))

	styles := readStaticFile(t, "../internal/publish/assets/public.css")
	if !strings.Contains(styles, ".public-rail--right .rail-chevron {\n  order: 2;") {
		t.Error("the right rail's arrow does not trail its label")
	}
	if strings.Contains(styles, ".public-rail--left .rail-chevron {\n  order:") {
		t.Error("the left rail's arrow should lead its label, not be ordered after it")
	}
}

// railLabelIn returns the text of the first rail label in the given rail class.
func railLabelIn(body string, railClass string) string {
	start := strings.Index(body, railClass)
	if start < 0 {
		return ""
	}

	rest := body[start:]
	labelAt := strings.Index(rest, `class="rail-label">`)
	if labelAt < 0 {
		return ""
	}

	rest = rest[labelAt+len(`class="rail-label">`):]
	end := strings.Index(rest, "</span>")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// between returns the text between two markers, or an empty string when the first is absent.
func between(body string, start, end string) string {
	from := strings.Index(body, start)
	if from < 0 {
		return ""
	}
	body = body[from+len(start):]

	to := strings.Index(body, end)
	if to < 0 {
		return ""
	}
	return body[:to]
}

// fixedPublicTime is a publication instant, so the rendered dates do not depend on the clock.
func fixedPublicTime(day int) time.Time {
	return time.Date(2026, time.March, day, 12, 0, 0, 0, time.UTC)
}
