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

			for _, path := range []string{"/dashboard", "/editor", "/library", "/caelum", "/share"} {
				want := "false"
				if path == tc.active {
					want = "true"
				}
				needle := `href="` + path + `" hx-get="` + path + `" hx-target="#screen" hx-swap="innerHTML" class="verse-masthead-link" data-masthead-link data-active="` + want + `"`
				if !strings.Contains(body, needle) {
					t.Fatalf("masthead link %q should have data-active=%q; body: %q", path, want, body)
				}
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
	if !strings.Contains(body, `class="verse-mobile-brand">Verse</a>`) {
		t.Fatalf("mobile nav brand missing from nav render: %q", body)
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
	if !strings.Contains(body, `class="verse-masthead-link"`) {
		t.Fatalf("masthead link class missing from nav render: %q", body)
	}
	if !strings.Contains(body, `id="masthead" class="verse-masthead" hx-swap-oob="outerHTML"`) {
		t.Fatalf("masthead oob shell missing from nav render: %q", body)
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
