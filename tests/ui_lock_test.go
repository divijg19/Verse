package tests

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	page "github.com/a-h/templ"
	views "github.com/divijg19/Verse/templ"
)

func TestSharedScreenLayoutLocksViewport(t *testing.T) {
	body := renderComponent(t, views.Layout("share", views.Share()))

	assertContainsAll(t, body,
		`<body class="bg-neutral-950 text-neutral-200 h-screen overflow-hidden">`,
		`id="viewport" class="verse-app-shell h-screen overflow-hidden"`,
		`[data-editor-overlay-active="true"] #screen {`,
		`z-index: 120;`,
		`.verse-mobile-screen-title,`,
		`.verse-mobile-screen-title-wrap,`,
		`.verse-mobile-copy-trim {`,
		`class="verse-mobile-current-surface">Share</span>`,
		`.verse-button,`,
		`.verse-button-danger {`,
		`min-width: 8.5rem;`,
		`min-height: 2.625rem;`,
		`border-radius: 0.375rem;`,
		`font-size: 1rem;`,
		`id="mobile-nav"`,
		`class="verse-mobile-nav-toggle"`,
		`data-mobile-nav-link`,
		`id="screen" class="verse-screen-frame verse-screen-frame--standard`,
		`class="verse-desktop-nav-button"`,
		`<div class="verse-surface-shell">`,
		`class="verse-surface-heading verse-mobile-screen-title"`,
		`>Share</h1>`,
	)
}

func TestEditorDefaultViewUsesFixedPanelAndPinnedActions(t *testing.T) {
	body := renderComponent(t, views.Editor(renderContext()))

	assertContainsAll(t, body,
		`<div class="verse-editor-screen">`,
		`.verse-editor-screen {`,
		`height: calc(100svh - (var(--verse-desktop-nav-rail-block, 2rem) * 2));`,
		`padding-top: var(--verse-surface-top-space, clamp(7rem, 16vh, 9rem));`,
		`.verse-editor-card {`,
		`height: clamp(calc(22rem - 30px), calc(52vh - 30px), calc(34rem - 30px));`,
		`flex: none;`,
		`.verse-editor-textarea {`,
		`resize: none;`,
		`.verse-editor-actions {`,
		`.verse-editor-status {`,
		`min-height: 1.25rem;`,
		`.verse-editor-submit {`,
		`min-width: 8.5rem;`,
		`min-height: 2.625rem;`,
		`font-size: 1rem;`,
		`.verse-editor-fullscreen-button {`,
		`class="verse-surface-heading verse-mobile-screen-title"`,
		`<span class="verse-editor-submit-text">Save</span>`,
		`<span class="verse-editor-saving">Saving</span>`,
		`@media (max-width: 1023px) {`,
		`<span>Full screen</span>`,
	)

	saveIndex := strings.Index(body, `<span class="verse-editor-submit-text">Save</span>`)
	fullscreenIndex := strings.Index(body, `<span>Full screen</span>`)
	if saveIndex == -1 || fullscreenIndex == -1 {
		t.Fatalf("editor actions missing expected buttons: %q", body)
	}
	if saveIndex > fullscreenIndex {
		t.Fatalf("editor action order changed, want Save before Full screen: %q", body)
	}
}

func TestEditorEditViewCarriesPoemIDIntoBothForms(t *testing.T) {
	body := renderComponent(t, views.EditorWithPoem(renderContext(), "poem-123", "A bell in snow"))

	needle := `<input type="hidden" name="id" value="poem-123">`
	if count := strings.Count(body, needle); count != 2 {
		t.Fatalf("editor edit view hidden poem id count = %d, want 2 in %q", count, body)
	}

	assertContainsAll(t, body,
		`>Edit</h1>`,
		`data-editor-base`,
		`data-editor-overlay-textarea`,
		`Focus Mode`,
		`>Close</button>`,
	)
}

func TestEditorFocusModeKeepsScrollInsideOverlayPanel(t *testing.T) {
	body := renderComponent(t, views.Editor(renderContext()))

	assertContainsAll(t, body,
		`data-editor-overlay class="verse-editor-overlay">`,
		`.verse-editor-overlay {`,
		`z-index: 130;`,
		`overflow: hidden;`,
		`.verse-editor-overlay-panel {`,
		`.verse-editor-overlay-card {`,
		`max-height: 100%;`,
		`.verse-editor-overlay-form {`,
		`.verse-editor-overlay-well {`,
		`class="verse-editor-textarea verse-editor-overlay-textarea w-full flex-1 min-h-0 overflow-y-auto`,
		`id="result-fullscreen" role="status" aria-live="polite" class="verse-editor-status text-sm text-neutral-400"`,
		`.verse-editor-root-frame.htmx-request .verse-editor-saving,`,
		`.verse-editor-overlay-form.htmx-request .verse-editor-saving {`,
		`.verse-editor-overlay-header {`,
		`flex-direction: column;`,
	)

	// The two statements that actually lock the viewport moved to static/js/editor.js in v0.4.8,
	// because the content security policy blocks an inline <script> and the previous version of this
	// test asserted they were in the rendered page. It could not have been right for long: those
	// statements were in the markup and therefore never ran in any browser, and this test passed
	// anyway. The assertion is kept, against the file the policy now permits.
	assertContainsAll(t, readStaticFile(t, filepath.Join("static", "js", "editor.js")),
		`viewport.dataset.editorOverlayActive = "true";`,
		`delete viewport.dataset.editorOverlayActive;`,
	)
}

// TestEditorOverlayStateDoesNotCollideWithItsOpenControl is the regression for the close path.
//
// The viewport used data-editor-overlay-open as its open/closed state flag, and the delegated click
// handler uses the same selector to find the Full screen button. The viewport is an ancestor of the
// overlay, so while the overlay was open every click inside it matched the viewport first, called
// the open path, and returned before the Close button or the backdrop could close anything. Escape
// still worked because its handler calls the close function directly. The state flag and the
// control selector must stay separate names.
func TestEditorOverlayStateDoesNotCollideWithItsOpenControl(t *testing.T) {
	layout := renderComponent(t, views.Layout("share", views.Share()))
	if !strings.Contains(layout, `[data-editor-overlay-active="true"] #screen {`) {
		t.Fatalf("layout does not style the viewport from data-editor-overlay-active: %q", layout)
	}

	js := readStaticFile(t, filepath.Join("static", "js", "editor.js"))
	if strings.Contains(js, `viewport.dataset.editorOverlayOpen`) {
		t.Fatalf("the viewport state flag reuses data-editor-overlay-open, which is also the open " +
			"control selector; every click inside an open overlay then matches the viewport ancestor " +
			"and the close control never runs")
	}

	assertContainsAll(t, js,
		`viewport.dataset.editorOverlayActive = "true";`,
		`delete viewport.dataset.editorOverlayActive;`,
		`verseClosest(event.target, "[data-editor-overlay-open]")`,
		`verseClosest(event.target, "[data-editor-overlay-close]")`,
	)

	// The overlay is a native dialog. The backdrop is the dialog's ::backdrop, and the close
	// path is the dialog's close event, so the panel no longer needs pointer-events tricks to
	// let clicks reach a backdrop element.
	body := renderComponent(t, views.Editor(renderContext()))
	assertContainsAll(t, body,
		`<dialog data-editor-overlay class="verse-editor-overlay">`,
		`.verse-editor-overlay::backdrop {`,
	)

	assertContainsAll(t, js,
		`overlay.showModal()`,
		`overlay.close()`,
		`editorOverlay.addEventListener("close"`,
	)
}

// readStaticFile reads a file from the repository's static directory, from either the repository root
// or the tests directory, since the suite runs with either as its working directory.
func readStaticFile(t *testing.T, path string) string {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		path = filepath.Join("..", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	return string(body)
}

func TestDashboardHeatmapScalesDownOnSmallerScreens(t *testing.T) {
	body := renderComponent(t, views.Heatmap(time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), nil))

	assertContainsAll(t, body,
		`--verse-heatmap-cell: 2rem;`,
		`grid-template-columns: repeat(7, var(--verse-heatmap-cell));`,
		`@media (max-width: 767px) {`,
		`--verse-heatmap-cell: 1.6rem;`,
		`@media (max-width: 479px) {`,
		`--verse-heatmap-cell: 1.35rem;`,
	)
}

func TestDashboardPlacesOverviewAboveRecentAndKeepsSidebarQuickActions(t *testing.T) {
	body := renderComponent(t, views.Dashboard(12, 4, nil, time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), nil))

	assertContainsAll(t, body,
		`class="verse-dashboard-shell verse-surface-body"`,
		`class="verse-dashboard-topline verse-mobile-screen-title-wrap"`,
		`class="verse-dashboard-overview-strip verse-panel-soft"`,
		`class="verse-dashboard-sidebar"`,
		`class="verse-dashboard-recent verse-panel"`,
		`class="verse-dashboard-activity-body"`,
		`class="verse-dashboard-sidebar-actions"`,
		`margin-top: auto;`,
		`class="verse-dashboard-quick-actions"`,
		`class="verse-dashboard-quick-action verse-dashboard-quick-action--primary"`,
		`class="verse-dashboard-quick-action verse-dashboard-quick-action--secondary"`,
		`class="verse-dashboard-stat verse-dashboard-overview-stat"`,
		`gap: clamp(1.85rem, 3vw, 2.35rem);`,
		`min-width: 8.5rem;`,
		`min-height: 2.625rem;`,
		`font-size: 1rem;`,
		`--verse-heatmap-cell: 1.98rem;`,
		`>Dashboard</h1>`,
		`>Total Poems</p>`,
		`>12</p>`,
		`>Current Streak</p>`,
		`>4</p>`,
		`>Recent</p>`,
		`>Activity</h2>`,
		`>Write</button>`,
		`>Look to Caelum</button>`,
		`>Library</button>`,
	)

	if strings.Contains(body, `>Move Through Verse</p>`) {
		t.Fatalf("dashboard still renders removed Move Through Verse card: %q", body)
	}
	if strings.Contains(body, `>Overview</p>`) {
		t.Fatalf("dashboard should no longer render the overview label text: %q", body)
	}

	overviewIndex := strings.Index(body, `class="verse-dashboard-overview-strip verse-panel-soft"`)
	recentIndex := strings.Index(body, `>Recent</p>`)
	activityIndex := strings.Index(body, `>Activity</h2>`)
	writeIndex := strings.Index(body, `>Write</button>`)
	if overviewIndex == -1 || recentIndex == -1 || activityIndex == -1 || writeIndex == -1 {
		t.Fatalf("dashboard missing expected overview/recent/activity/action markers: %q", body)
	}
	if overviewIndex > recentIndex {
		t.Fatalf("dashboard overview strip should render above the recent card: %q", body)
	}
	if writeIndex < recentIndex {
		t.Fatalf("dashboard quick actions should render below the recent card: %q", body)
	}
	if writeIndex > activityIndex {
		t.Fatalf("dashboard quick actions should stay in the left sidebar before the activity card markup: %q", body)
	}
}

func TestCaelumUsesCompactPromptSurface(t *testing.T) {
	body := renderComponent(t, views.Caelum())

	assertContainsAll(t, body,
		`>Caelum</h1>`,
		`class="verse-surface-heading verse-mobile-screen-title"`,
		`class="verse-caelum-panel verse-panel"`,
		`>Prompt</p>`,
		`id="prompt" class="verse-caelum-prompt-output italic"`,
		`>Generate Prompt</button>`,
		`>Open Editor</button>`,
	)

	for _, removed := range []string{
		`>Prompt Field</p>`,
		`>Use It Well</p>`,
		`Caelum offers a fresh poetic direction`,
		`Move from prompt to editor without changing context`,
	} {
		if strings.Contains(body, removed) {
			t.Fatalf("caelum should stay compact without redundant copy, found %q in %q", removed, body)
		}
	}
}

func TestShareUsesCompactModeSurface(t *testing.T) {
	body := renderComponent(t, views.Share())

	assertContainsAll(t, body,
		`>Share</h1>`,
		`class="verse-surface-heading verse-mobile-screen-title"`,
		`class="verse-share-panel verse-panel"`,
		`>Quiet Copy</p>`,
		`>Card Export</p>`,
		`>Private Link</p>`,
		`>Open Library</button>`,
		`>Write Again</button>`,
	)

	for _, removed := range []string{
		`>Share Studio</p>`,
		`>Until Then</p>`,
		`This surface now reads as a complete destination`,
		`Keep writing and refining.`,
	} {
		if strings.Contains(body, removed) {
			t.Fatalf("share should stay compact without redundant copy, found %q in %q", removed, body)
		}
	}
}

func TestLibraryUsesInternalResultsScrollRegion(t *testing.T) {
	body := renderComponent(t, views.Library("", nil))

	assertContainsAll(t, body,
		`<div class="verse-library-screen">`,
		`class="verse-surface-heading verse-mobile-screen-title"`,
		`>Library</h1>`,
		`.verse-library-shell {`,
		`height: 100%;`,
		`min-height: 0;`,
		`class="verse-library-shell not-prose overflow-hidden pt-1 pb-4"`,
		`id="library-results" class="verse-library-results-pane overflow-y-auto scroll-smooth pr-2 pt-0 relative"`,
		`class="verse-mobile-copy-trim mx-auto max-w-lg text-sm leading-7 text-neutral-400"`,
		`This library is empty.`,
	)
}

func TestSurfaceRoutesStillReturnLockedScreenShell(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	defer srv.Close()

	for _, path := range []string{"/editor", "/library", "/share"} {
		status, body, _ := get(t, srv.URL+path, nil)
		if status != 200 {
			t.Fatalf("GET %s status = %d, want 200", path, status)
		}

		assertContainsAll(t, body,
			`<body class="bg-neutral-950 text-neutral-200 h-screen overflow-hidden">`,
			`id="viewport" class="verse-app-shell h-screen overflow-hidden"`,
			`id="screen" class="verse-screen-frame`,
		)
	}
}

// renderContext returns a context carrying a CSRF token, mirroring an authenticated request.
// Components that render a mutating form now emit the token field, so a bare context would produce
// markup that differs from anything the application actually serves.
func renderContext() context.Context {
	return views.WithCSRFToken(context.Background(), "test-csrf-token")
}

func renderComponent(t *testing.T, component page.Component) string {
	t.Helper()

	var buf bytes.Buffer
	if err := component.Render(renderContext(), &buf); err != nil {
		t.Fatalf("render component failed: %v", err)
	}

	return buf.String()
}

func assertContainsAll(t *testing.T, body string, needles ...string) {
	t.Helper()

	for _, needle := range needles {
		if !strings.Contains(body, needle) {
			t.Fatalf("rendered output missing %q in %q", needle, body)
		}
	}
}
