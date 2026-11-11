package tests

import (
	"bytes"
	"context"
	"strings"
	"testing"

	views "github.com/divijg19/Verse/templ"
)

func TestNavigationMatrix(t *testing.T) {
	cases := []struct {
		surface string
		active  string
	}{
		{surface: "dashboard", active: "/dashboard"},
		{surface: "editor", active: "/editor"},
		{surface: "library", active: "/library"},
		{surface: "caelum", active: "/caelum"},
		{surface: "share", active: "/share"},
	}

	for _, tc := range cases {
		t.Run(tc.surface, func(t *testing.T) {
			body := renderNavOOB(t, tc.surface)

			// Find every desktop nav <a> element and verify its data-active attribute.
			remaining := body
			for {
				aStart := strings.Index(remaining, `<a `)
				if aStart == -1 {
					break
				}
				aEnd := strings.Index(remaining[aStart:], `>`)
				if aEnd == -1 {
					break
				}
				aEnd = aStart + aEnd + 1
				aHTML := remaining[aStart : aEnd+1]

				// Extract href
				hrefIdx := strings.Index(aHTML, `href="`)
				if hrefIdx == -1 {
					remaining = remaining[aStart+2:]
					continue
				}
				afterHref := aHTML[hrefIdx+len(`href="`):]
				endQuote := strings.Index(afterHref, `"`)
				if endQuote == -1 {
					remaining = remaining[aStart+2:]
					continue
				}
				path := afterHref[:endQuote]

				// Extract data-active from the same <a> element.
				activeIdx := strings.Index(aHTML, `data-active="`)
				if activeIdx == -1 {
					t.Fatalf("nav button %q missing data-active attribute; HTML: %q", path, aHTML)
				}
				afterActive := aHTML[activeIdx+len(`data-active="`):]
				activeEnd := strings.Index(afterActive, `"`)
				if activeEnd == -1 {
					t.Fatalf("nav button %q has unterminated data-active; HTML: %q", path, aHTML)
				}
				active := afterActive[:activeEnd]

				want := "false"
				if path == tc.active {
					want = "true"
				}
				if active != want {
					t.Fatalf("nav button %q has data-active=%q, want %q; HTML: %q", path, active, want, aHTML)
				}

				remaining = remaining[aStart+2:]
			}
		})
	}
}

func TestNavigationOOBIncludesMobileNavigationShell(t *testing.T) {
	body := renderNavOOB(t, "editor")

	if !strings.Contains(body, `id="mobile-nav" hx-swap-oob="outerHTML"`) {
		t.Fatalf("mobile nav oob shell missing from nav render: %q", body)
	}
	if !strings.Contains(body, `data-mobile-nav-sheet`) {
		t.Fatalf("mobile nav sheet markup missing from nav render: %q", body)
	}
	if !strings.Contains(body, `class="verse-mobile-current-surface">Editor</span>`) {
		t.Fatalf("mobile nav current surface label missing from nav render: %q", body)
	}
	if !strings.Contains(body, `data-mobile-nav-link`) {
		t.Fatalf("mobile nav link hooks missing from nav render: %q", body)
	}
	for _, removed := range []string{
		`>Navigate</p>`,
		`>Overview and activity</span>`,
		`>Browse your archive</span>`,
		`>Draw a fresh prompt</span>`,
		`>Prepare work for export</span>`,
	} {
		if strings.Contains(body, removed) {
			t.Fatalf("mobile nav should no longer render redundant copy %q in %q", removed, body)
		}
	}
	if !strings.Contains(body, `class="verse-desktop-nav-button"`) {
		t.Fatalf("desktop nav button class missing from nav render: %q", body)
	}
	if !strings.Contains(body, `id="nav-top" hx-swap-oob="outerHTML"`) {
		t.Fatalf("nav-top slot missing from nav render: %q", body)
	}
}

func renderNavOOB(t *testing.T, surface string) string {
	t.Helper()

	var buf bytes.Buffer
	if err := views.NavOOB(surface).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render nav failed: %v", err)
	}

	return buf.String()
}
