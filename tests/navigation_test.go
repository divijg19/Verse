package tests

import (
	"bytes"
	"context"
	"strings"
	"testing"

	views "github.com/divijg19/Verse/templ"
)

func TestNavigationMatrix(t *testing.T) {
	type navCase struct {
		surface string
		top     string
		left    string
		right   string
		bottom  string
	}

	cases := []navCase{
		{surface: "dashboard", top: "/caelum", left: "", right: "/editor", bottom: "/share"},
		{surface: "editor", top: "/caelum", left: "/dashboard", right: "/library", bottom: "/share"},
		{surface: "library", top: "/caelum", left: "/editor", right: "", bottom: "/share"},
		{surface: "caelum", top: "", left: "/dashboard", right: "/library", bottom: "/editor"},
		{surface: "share", top: "/editor", left: "/dashboard", right: "/library", bottom: ""},
	}

	for _, tc := range cases {
		t.Run(tc.surface, func(t *testing.T) {
			body := renderNavOOB(t, tc.surface)

			assertRenderedSlotPath(t, body, "nav-top", tc.top)
			assertRenderedSlotPath(t, body, "nav-left", tc.left)
			assertRenderedSlotPath(t, body, "nav-right", tc.right)
			assertRenderedSlotPath(t, body, "nav-bottom", tc.bottom)
		})
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

func assertRenderedSlotPath(t *testing.T, body, slotID, expectedPath string) {
	t.Helper()

	marker := `id="` + slotID + `" hx-swap-oob="outerHTML"`
	start := strings.Index(body, marker)
	if start == -1 {
		t.Fatalf("missing slot marker %s", marker)
	}

	fragment := body[start:]
	end := strings.Index(fragment, "</div>")
	if end == -1 {
		t.Fatalf("missing closing div for slot %s", slotID)
	}
	slot := fragment[:end]

	if expectedPath == "" {
		if strings.Contains(slot, `hx-get="`) {
			t.Fatalf("slot %s unexpectedly contains link: %q", slotID, slot)
		}
		return
	}

	needle := `hx-get="` + expectedPath + `"`
	if !strings.Contains(slot, needle) {
		t.Fatalf("slot %s missing %q in %q", slotID, needle, slot)
	}
}
