package tests

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	views "github.com/divijg19/Verse/templ"
)

// hostileInputs are payloads that break out of an HTML attribute or element if escaping fails.
var hostileInputs = []string{
	`"><script>alert(1)</script>`,
	`" onmouseover="alert(1)`,
	`"><img src=x onerror=alert(1)>`,
	`</textarea><script>alert(1)</script>`,
	`javascript:alert(1)`,
	`'; alert(1); //`,
}

// TestAttributeValuesAreEscaped is the permanent regression test for the templ generator-version
// mismatch found in the v0.3.6 audit.
//
// The committed generated code was produced by templ v0.3.1001 while go.mod pins v0.3.1020. The
// regeneration delta changes how attribute values are resolved — specifically, the generated call
// changed from templ.EscapeString(x) to templ.ResolveAttributeValue(x) — and the library renders a
// user-supplied search term directly into an attribute (value={ query }). The behavior was verified
// to be correct under both generator versions, but nothing enforced it, so a future templ upgrade
// could silently change how attribute values are escaped without any test noticing.
func TestAttributeValuesAreEscaped(t *testing.T) {
	for _, hostile := range hostileInputs {
		t.Run(shortLabel(hostile), func(t *testing.T) {
			var buf bytes.Buffer
			if err := views.Library(hostile, nil).Render(context.Background(), &buf); err != nil {
				t.Fatalf("render library: %v", err)
			}
			body := buf.String()

			assertNoRawInjection(t, body, hostile)
			assertSearchValuePresent(t, body, hostile)
		})
	}
}

// TestPoemIdentifiersAreEscapedInAttributes covers the second attribute that carries dynamic data,
// the per-work link and title rendered by the library row.
func TestPoemIdentifiersAreEscapedInAttributes(t *testing.T) {
	hostile := `"><script>alert(1)</script>`

	var buf bytes.Buffer
	component := views.PoemItem(views.PoemView{
		ID:        hostile,
		Title:     hostile,
		Snippet:   hostile,
		CreatedAt: time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC),
	})
	if err := component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render poem item: %v", err)
	}

	assertNoRawInjection(t, buf.String(), hostile)
}

// assertNoRawInjection fails if the payload appears verbatim, which would mean it broke out of its
// attribute and could execute.
//
// The check is deliberately about the payload as a contiguous unit rather than about suspicious
// substrings. A payload such as `"><img src=x onerror=alert(1)>` correctly renders as
// `&lt;img src=x onerror=alert(1)&gt;` — the substring "onerror=alert(1)" is still present, but it is
// inert text inside a quoted attribute, not an attribute on an element. Matching on the substring
// would therefore fail correct output. Likewise `javascript:` carries no characters requiring
// escaping and is harmless in a value attribute, which is where this input lands.
func assertNoRawInjection(t *testing.T, body, hostile string) {
	t.Helper()

	// Only a payload containing a character that requires escaping can demonstrate a failure by
	// appearing verbatim. A payload such as "javascript:alert(1)" has no escapable character, so its
	// verbatim presence in a quoted value attribute is both correct and inert.
	if needsEscaping(hostile) && strings.Contains(body, hostile) {
		t.Fatalf("payload appeared verbatim in the output, so it was not escaped: %q", hostile)
	}
	if strings.Contains(body, "<script") {
		t.Fatal("a script element reached the rendered output")
	}
	// The escaped form must not have been double-decoded anywhere.
	if strings.Contains(body, "<img src=x") {
		t.Fatal("an img element reached the rendered output")
	}
}

// assertSearchValuePresent verifies the value is still present, escaped. Without this, a component
// that silently dropped the attribute would pass the injection check.
func assertSearchValuePresent(t *testing.T, body, hostile string) {
	t.Helper()

	escaped := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
		"'", "&#39;",
	).Replace(hostile)

	if !strings.Contains(body, escaped) {
		t.Fatalf("the search value is absent even in escaped form; the attribute may have been dropped")
	}
}

// needsEscaping reports whether a payload contains any character templ must escape in HTML.
func needsEscaping(s string) bool {
	return strings.ContainsAny(s, "<>\"&'")
}

func shortLabel(s string) string {
	if len(s) > 24 {
		return s[:24]
	}
	return s
}
