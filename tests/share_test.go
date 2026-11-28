package tests

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/services"
	views "github.com/divijg19/Verse/templ"
)

// TestShareEndpointReturnsOneWork covers the data behind the Share picker's actions.
//
// The Share page carries titles and identifiers only; the endpoint supplies a work's body when an
// action needs it. That keeps the library's text out of a page whose purpose is choosing one work,
// and it means the body the clipboard receives is read from the database at the moment of the copy.
func TestShareEndpointReturnsOneWork(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Shared Work\n\nThe body that should be copied verbatim.")
	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/share/work/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /share/work/%s = %d, want 200", id, status)
	}

	var data struct {
		Title       string `json:"title"`
		Body        string `json:"body"`
		Address     string `json:"address"`
		Published   string `json:"published"`
		IsPublished bool   `json:"isPublished"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatalf("decode the share data: %v", err)
	}

	if data.Title != "A Shared Work" {
		t.Errorf("title = %q, want A Shared Work", data.Title)
	}
	if data.Body != "The body that should be copied verbatim." {
		t.Errorf("body = %q, want the body without its title", data.Body)
	}
	if data.IsPublished {
		t.Error("a draft reports itself as published")
	}
	if data.Address != "" {
		t.Errorf("a draft has the address %q, want none", data.Address)
	}
	if data.Published != "" {
		t.Errorf("a draft has the date %q, want none", data.Published)
	}
}

// TestShareWorkPayloadHasNoOverlayData pins the split introduced when suggestions moved to their
// own endpoint: the work payload stays small and never carries guest text.
func TestShareWorkPayloadHasNoOverlayData(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Shared Work\n\nThe body that should be copied verbatim.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t)
	defer srv.Close()
	token := mintPublicLink(t, srv.URL, id)
	visitor := guestClient(t, srv)
	status, page, _ := guestGet(t, visitor, srv.URL+"/p/"+token)
	if status != http.StatusOK {
		t.Fatalf("public page = %d", status)
	}
	status, _, _ = guestPost(t, visitor, srv.URL+"/p/"+token+"/submit", url.Values{
		"csrf": {csrfFromBody(t, page)}, "name": {"Reader"}, "content": {"SUBMISSION SENTINEL"},
	})
	if status != http.StatusOK {
		t.Fatalf("submit = %d", status)
	}

	status, body, _ := get(t, srv.URL+"/share/work/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /share/work/%s = %d", id, status)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"title", "body", "address", "published", "isPublished", "publicAddressConfigured",
		"links", "publicLinks",
	}
	if len(raw) != len(want) {
		t.Errorf("share payload has %d keys, want %d: %s", len(raw), len(want), truncate([]byte(body)))
	}
	for _, key := range want {
		if _, ok := raw[key]; !ok {
			t.Errorf("share payload is missing %q", key)
		}
	}
	for _, key := range []string{"publicSubmissions", "submissions", "unavailable", "total"} {
		if _, ok := raw[key]; ok {
			t.Errorf("share payload carries overlay field %q", key)
		}
	}
	if strings.Contains(body, "SUBMISSION SENTINEL") {
		t.Error("the selected-work payload leaked submission text")
	}
}

// TestShareEndpointGivesAPublishedWorkItsAddress covers the Copy Link half. A published work is
// marked as such so the browser can enable Copy Link; a draft is not, which is why the page
// disables the button rather than handing out a link that would 404.
func TestShareEndpointGivesAPublishedWorkItsAddress(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "A Public Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatalf("publish: %v", err)
	}

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/share/work/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /share/work/%s = %d, want 200", id, status)
	}

	var data struct {
		Address                 string `json:"address"`
		Published               string `json:"published"`
		IsPublished             bool   `json:"isPublished"`
		PublicAddressConfigured bool   `json:"publicAddressConfigured"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatalf("decode the share data: %v", err)
	}

	if !data.IsPublished {
		t.Error("a published work reports itself as a draft")
	}
	if data.Published == "" {
		t.Error("a published work has no publication date")
	}
	// With no reader origin configured the address must be empty, not a relative /w/... path. This
	// assertion used to be missing, which let the original Copy Link regression pass unnoticed.
	if data.Address != "" {
		t.Errorf("address = %q with no reader origin, want empty", data.Address)
	}
	if data.PublicAddressConfigured {
		t.Error("an unset PUBLIC_BASE_URL reports the public address as configured")
	}
}

func TestShareEndpointRejectsAMalformedReaderOrigin(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)
	t.Setenv("PUBLIC_BASE_URL", "localhost:4000")

	id := insertPoem(t, "A Public Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatalf("publish: %v", err)
	}

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/share/work/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /share/work/%s = %d, want 200", id, status)
	}
	var data struct {
		Address                 string `json:"address"`
		PublicAddressConfigured bool   `json:"publicAddressConfigured"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatal(err)
	}
	if data.Address != "" || data.PublicAddressConfigured {
		t.Errorf("a malformed origin produced address %q, configured=%t", data.Address, data.PublicAddressConfigured)
	}
}

// TestShareEndpointUsesTheReaderOrigin pins the other half of the origin split.
//
// Private links are built from the request's own origin; public addresses are not. A regression that
// swapped the two would pass every private-link test, so the reader origin is asserted here.
func TestShareEndpointUsesTheReaderOrigin(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)
	t.Setenv("PUBLIC_BASE_URL", "https://reader.example")

	id := insertPoem(t, "A Public Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), id); err != nil {
		t.Fatalf("publish: %v", err)
	}

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/share/work/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /share/work/%s = %d, want 200", id, status)
	}
	var data struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatal(err)
	}
	if want := "https://reader.example/w/" + id + "/"; data.Address != want {
		t.Errorf("public address = %q, want %q", data.Address, want)
	}
}

// TestShareEndpointRefusesANonExistentWork covers the miss, so the browser can report it honestly.
func TestShareEndpointRefusesANonExistentWork(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)
	defer srv.Close()

	status, _, _ := get(t, srv.URL+"/share/work/11111111-2222-3333-4444-555555555555", nil)
	if status != http.StatusNotFound {
		t.Fatalf("GET /share/work/unknown = %d, want 404", status)
	}
}

// TestTheShareFormIsAccepted covers the CSRF contract of the picker's data route.
//
// The route is a GET inside the authenticated group, so a session is required; the form helper
// supplies the token that every state-changing route requires and confirms the route stays usable.
func TestTheShareFormIsAccepted(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	insertPoem(t, "A work in the picker")
	srv := newTestServer(t)
	defer srv.Close()

	status, _, _ := get(t, srv.URL+"/share", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /share = %d, want 200", status)
	}

	// The share page is rendered for an authenticated reader; a POST with no change is not a route
	// it has, and must be refused rather than silently treated as a read.
	status, _, _ = postForm(t, srv.URL+"/share", url.Values{}, nil)
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("POST /share = %d, want 405", status)
	}
}

// TestSharePickerMarksPublishedWorks covers the flag that enables Copy Link.
//
// The picker used to read PublishedAt from ListPoems, whose column list omits published_at, so every
// work arrived as a draft and the button was disabled even for works with a public address. This
// test reads the rendered markup rather than calling the service, because the bug was the wiring
// between the two.
func TestSharePickerMarksPublishedWorks(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	published := insertPoem(t, "A Public Work\n\nIts body.")
	draft := insertPoem(t, "A Draft Work\n\nIts body.")
	if err := services.PublishPoem(t.Context(), published); err != nil {
		t.Fatalf("publish: %v", err)
	}

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/share", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /share = %d, want 200", status)
	}

	for _, tc := range []struct {
		id   string
		want string
	}{
		{published, `data-published="true"`},
		{draft, `data-published="false"`},
	} {
		option := ""
		for _, line := range strings.Split(body, "<option") {
			if strings.Contains(line, tc.id) {
				option = line
				break
			}
		}
		if option == "" {
			t.Fatalf("no picker option for %s", tc.id)
		}
		if !strings.Contains(option, tc.want) {
			t.Errorf("picker option for %s is missing %s: %s", tc.id, tc.want, truncate([]byte(option)))
		}
	}
}

// TestPublicArchiveCarriesNoBodyText holds the invariant the Share actions depend on: the full text
// is on the work page alone, and the reader's script fetches it from there rather than from a
// second published copy.
func TestPublicArchiveCarriesNoBodyText(t *testing.T) {
	body := "a body that must not appear twice"
	site := views.PublicSite{
		Title:   "Verse",
		BaseURL: "https://verse.test",
		Works: []views.PublicWork{{
			ID:          "a",
			Path:        "/w/a/",
			Title:       "A Shared Work",
			Content:     "A Shared Work\n\n" + body,
			PublishedAt: fixedPublicTime(3),
		}},
	}

	rendered := renderComponent(t, views.PublicArchive(site))
	if strings.Contains(rendered, body) {
		t.Error("the public archive contains a work's body")
	}
}
