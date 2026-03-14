package tests

import (
	"bytes"
	"context"
	"strings"
	"testing"

	page "github.com/a-h/templ"
	views "github.com/divijg19/Verse/templ"
)

func TestSharedScreenLayoutLocksViewport(t *testing.T) {
	body := renderComponent(t, views.LayoutWithSurface("share", views.Share()))

	assertContainsAll(t, body,
		`<body class="bg-neutral-950 text-neutral-200 h-screen overflow-hidden">`,
		`id="viewport" class="h-screen overflow-hidden flex items-start justify-center"`,
		`id="screen" class="h-full max-w-3xl w-full overflow-hidden`,
		`<div class="h-full overflow-hidden space-y-6" style="padding-top: clamp(7rem, 16vh, 9rem);">`,
		`>Share</h1>`,
	)
}

func TestEditorDefaultViewUsesFixedPanelAndPinnedActions(t *testing.T) {
	body := renderComponent(t, views.Editor())

	assertContainsAll(t, body,
		`<div class="flex flex-col min-h-0 space-y-6" style="height: calc(100svh - 4rem); padding-top: clamp(7rem, 16vh, 9rem);">`,
		`.verse-editor-card {`,
		`height: clamp(calc(22rem - 30px), calc(52vh - 30px), calc(34rem - 30px));`,
		`flex: none;`,
		`.verse-editor-textarea {`,
		`resize: none;`,
		`grid-template-columns: minmax(0, 1fr) auto auto;`,
		`>Save Bloom</button>`,
		`<span>Full screen</span>`,
	)

	saveIndex := strings.Index(body, `>Save Bloom</button>`)
	fullscreenIndex := strings.Index(body, `<span>Full screen</span>`)
	if saveIndex == -1 || fullscreenIndex == -1 {
		t.Fatalf("editor actions missing expected buttons: %q", body)
	}
	if saveIndex > fullscreenIndex {
		t.Fatalf("editor action order changed, want Save Bloom before Full screen: %q", body)
	}
}

func TestEditorEditViewCarriesPoemIDIntoBothForms(t *testing.T) {
	body := renderComponent(t, views.EditorWithPoem("poem-123", "A bell in snow"))

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
	body := renderComponent(t, views.Editor())

	assertContainsAll(t, body,
		`data-editor-overlay class="verse-editor-overlay" hidden`,
		`.verse-editor-overlay {`,
		`overflow: hidden;`,
		`.verse-editor-overlay-panel {`,
		`.verse-editor-overlay-card {`,
		`max-height: 100%;`,
		`.verse-editor-overlay-form {`,
		`.verse-editor-overlay-well {`,
		`class="verse-editor-textarea verse-editor-overlay-textarea w-full flex-1 min-h-0 overflow-y-auto`,
	)
}

func TestLibraryUsesInternalResultsScrollRegion(t *testing.T) {
	body := renderComponent(t, views.Library("", nil))

	assertContainsAll(t, body,
		`<div class="space-y-6" style="height: calc(100svh - 4rem); padding-top: clamp(7rem, 16vh, 9rem);">`,
		`>Library</h1>`,
		`.verse-library-shell {`,
		`height: 100%;`,
		`min-height: 0;`,
		`class="verse-library-shell not-prose space-y-10 overflow-hidden pt-1 pb-4"`,
		`id="library-results" class="verse-library-results-pane overflow-y-auto scroll-smooth pr-2 pt-1"`,
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
			`id="viewport" class="h-screen overflow-hidden flex items-start justify-center"`,
			`id="screen" class="h-full`,
		)
	}
}

func renderComponent(t *testing.T, component page.Component) string {
	t.Helper()

	var buf bytes.Buffer
	if err := component.Render(context.Background(), &buf); err != nil {
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
