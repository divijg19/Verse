package publish_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/publish"
	publictempl "github.com/divijg19/Verse/templ"
)

func fixedTime(day int) time.Time {
	return time.Date(2026, time.March, day, 12, 0, 0, 0, time.UTC)
}

func sampleWorks() []publictempl.PublicWork {
	return []publictempl.PublicWork{
		{
			ID:          "11111111-1111-1111-1111-111111111111",
			Path:        "/w/11111111-1111-1111-1111-111111111111/",
			Title:       "The First Work",
			Content:     "The First Work\n\nA body that belongs to the first.",
			Excerpt:     "The First Work A body that belongs to the first.",
			PublishedAt: fixedTime(3),
		},
		{
			ID:          "22222222-2222-2222-2222-222222222222",
			Path:        "/w/22222222-2222-2222-2222-222222222222/",
			Title:       "The Second Work",
			Content:     "The Second Work\n\nA body that belongs to the second.",
			Excerpt:     "The Second Work A body that belongs to the second.",
			PublishedAt: fixedTime(2),
		},
	}
}

func testOptions() publish.Options {
	return publish.Options{
		Title:       "Verse",
		Description: "A selection of published work.",
		BaseURL:     "https://verse.test",
	}
}

func TestRenderProducesTheRequiredSurface(t *testing.T) {
	files, err := publish.Render(sampleWorks(), testOptions())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, required := range []string{
		"index.html",
		"archive/index.html",
		"404.html",
		"search.json",
		"sitemap.xml",
		"feed.xml",
		"robots.txt",
		"w/11111111-1111-1111-1111-111111111111/index.html",
		"w/22222222-2222-2222-2222-222222222222/index.html",
	} {
		if _, ok := files[required]; !ok {
			t.Errorf("missing output %q", required)
		}
	}

	hasStylesheet := false
	for name := range files {
		if strings.HasPrefix(name, "assets/") && strings.HasSuffix(name, ".css") {
			hasStylesheet = true
		}
	}
	if !hasStylesheet {
		t.Error("no hashed stylesheet under assets/")
	}

	if err := publish.Validate(files, "https://verse.test"); err != nil {
		t.Errorf("the rendered build did not validate: %v", err)
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	first, err := publish.Render(sampleWorks(), testOptions())
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := publish.Render(sampleWorks(), testOptions())
	if err != nil {
		t.Fatalf("second render: %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatal("two renders from identical input differ")
	}
}

func TestRenderEscapesHostileContent(t *testing.T) {
	hostile := `"><script>alert(1)</script>`
	works := []publictempl.PublicWork{{
		ID:          "33333333-3333-3333-3333-333333333333",
		Path:        "/w/33333333-3333-3333-3333-333333333333/",
		Title:       hostile,
		Content:     hostile + "\n\n" + `"><img src=x onerror=alert(1)>`,
		Excerpt:     hostile,
		PublishedAt: fixedTime(1),
	}}

	files, err := publish.Render(works, testOptions())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	page := string(files["w/33333333-3333-3333-3333-333333333333/index.html"])
	if strings.Contains(page, "<script") {
		t.Error("the work page contains a script element")
	}
	if strings.Contains(page, "<img src=x") {
		t.Error("the work page contains an unescaped image element from content")
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Error("the work page does not contain the escaped form of the hostile content")
	}

	search := string(files["search.json"])
	if strings.Contains(search, "<script") {
		t.Error("search.json contains an unescaped script element")
	}
	if !strings.Contains(search, `\u003c`) {
		t.Error("search.json does not escape HTML-significant characters")
	}
}

// TestValidateDoesNotRejectMarkersInsideProse is the counterweight to the leak scan. Work content is
// arbitrary: a poem may mention a path that looks like an authoring route, and rejecting it would
// make the publisher refuse legitimate art. Structure is what the scan checks; text is not.
func TestValidateDoesNotRejectMarkersInsideProse(t *testing.T) {
	works := sampleWorks()
	works[0].Content = "A Poem About Software\n\nIt mentions /library, /poem/, and hx-get, but only as text."
	works[0].Excerpt = "It mentions /library and hx-get."

	files, err := publish.Render(works, testOptions())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := publish.Validate(files, "https://verse.test"); err != nil {
		t.Fatalf("validation rejected legitimate prose: %v", err)
	}
}

func TestSearchIndexIsTrimmed(t *testing.T) {
	long := strings.Repeat("word ", 100)
	works := []publictempl.PublicWork{{
		ID:          "44444444-4444-4444-4444-444444444444",
		Path:        "/w/44444444-4444-4444-4444-444444444444/",
		Title:       "A Long Work",
		Content:     "A Long Work\n\n" + long,
		Excerpt:     strings.Repeat("word ", 40),
		PublishedAt: fixedTime(4),
	}}

	files, err := publish.Render(works, testOptions())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var index struct {
		Version int `json:"version"`
		Entries []struct {
			ID        string `json:"id"`
			Path      string `json:"path"`
			Title     string `json:"title"`
			Published string `json:"published"`
			Excerpt   string `json:"excerpt"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(files["search.json"], &index); err != nil {
		t.Fatalf("unmarshal search.json: %v", err)
	}

	if index.Version != 1 {
		t.Errorf("search index version = %d, want 1", index.Version)
	}
	if len(index.Entries) != 1 {
		t.Fatalf("search index has %d entries, want 1", len(index.Entries))
	}
	if got := len([]rune(index.Entries[0].Excerpt)); got != len([]rune(works[0].Excerpt)) {
		t.Errorf("excerpt was not preserved exactly: %d runes", got)
	}
	if strings.Contains(string(files["search.json"]), strings.Repeat("word ", 60)) {
		t.Error("search.json contains more body text than the configured excerpt")
	}
}

func TestSitemapAndFeedUseTheConfiguredBase(t *testing.T) {
	files, err := publish.Render(sampleWorks(), testOptions())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	sitemap := string(files["sitemap.xml"])
	for _, want := range []string{
		"https://verse.test/",
		"https://verse.test/archive/",
		"https://verse.test/w/11111111-1111-1111-1111-111111111111/",
		"<lastmod>2026-03-03</lastmod>",
	} {
		if !strings.Contains(sitemap, want) {
			t.Errorf("sitemap does not contain %q", want)
		}
	}

	feed := string(files["feed.xml"])
	for _, want := range []string{
		"<title>The First Work</title>",
		"https://verse.test/w/11111111-1111-1111-1111-111111111111/",
		"<pubDate>Tue, 03 Mar 2026 12:00:00 +0000</pubDate>",
	} {
		if !strings.Contains(feed, want) {
			t.Errorf("feed does not contain %q", want)
		}
	}
}

func TestValidateRejectsLeaksAndBrokenLinks(t *testing.T) {
	base, err := publish.Render(sampleWorks(), testOptions())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	t.Run("missing required file", func(t *testing.T) {
		files := copyFiles(base)
		delete(files, "404.html")
		if err := publish.Validate(files, "https://verse.test"); err == nil {
			t.Fatal("validation accepted a build without 404.html")
		}
	})

	t.Run("authoring route", func(t *testing.T) {
		files := copyFiles(base)
		files["index.html"] = append(files["index.html"], []byte(`<a href="/library">Library</a>`)...)
		if err := publish.Validate(files, "https://verse.test"); err == nil {
			t.Fatal("validation accepted a link to the authoring library")
		}
	})

	t.Run("htmx attribute", func(t *testing.T) {
		files := copyFiles(base)
		files["index.html"] = append(files["index.html"], []byte(`<div hx-get="/library"></div>`)...)
		if err := publish.Validate(files, "https://verse.test"); err == nil {
			t.Fatal("validation accepted an htmx attribute")
		}
	})

	t.Run("script element", func(t *testing.T) {
		files := copyFiles(base)
		files["index.html"] = append(files["index.html"], []byte(`<script>alert(1)</script>`)...)
		if err := publish.Validate(files, "https://verse.test"); err == nil {
			t.Fatal("validation accepted a script element")
		}
	})

	t.Run("broken internal link", func(t *testing.T) {
		files := copyFiles(base)
		files["index.html"] = bytes.ReplaceAll(files["index.html"],
			[]byte(`href="/archive/"`), []byte(`href="/missing/"`))
		if err := publish.Validate(files, "https://verse.test"); err == nil {
			t.Fatal("validation accepted a broken internal link")
		}
	})
}

// TestValidateHoldsThisSiteAndOtherSitesToDifferentRules checks the leak rules.
//
// The authoring paths are this application's routes, so they are a leak wherever they appear in an
// address that resolves here. An address naming another host is that host's business: the portfolio
// menu links outward on purpose, and a link to https://example.com/library/ is not a leak of
// anything. An address naming this site's own host is checked, because on this host the path means
// exactly what it means everywhere else.
func TestValidateHoldsThisSiteAndOtherSitesToDifferentRules(t *testing.T) {
	base, err := publish.Render(sampleWorks(), testOptions())
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	t.Run("an external address with an authoring-shaped path", func(t *testing.T) {
		files := copyFiles(base)
		files["index.html"] = append(files["index.html"],
			[]byte(`<a href="https://studio.example/library/">Studio</a>`)...)
		if err := publish.Validate(files, "https://verse.test"); err != nil {
			t.Fatalf("validation rejected an external address: %v", err)
		}
	})

	t.Run("an absolute address on this site with an authoring path", func(t *testing.T) {
		files := copyFiles(base)
		files["index.html"] = append(files["index.html"],
			[]byte(`<a href="https://verse.test/library/">Library</a>`)...)
		if err := publish.Validate(files, "https://verse.test"); err == nil {
			t.Fatal("validation accepted an authoring path on this site's own host")
		}
	})

	t.Run("the authoring host", func(t *testing.T) {
		files := copyFiles(base)
		files["index.html"] = append(files["index.html"],
			[]byte(`<a href="https://verse.onrender.com/">Authoring</a>`)...)
		if err := publish.Validate(files, "https://verse.test"); err == nil {
			t.Fatal("validation accepted a link to the authoring host")
		}
	})
}

func TestWriteDirReplacesOnlyAfterACompleteBuild(t *testing.T) {
	out := filepath.Join(t.TempDir(), "dist")

	if err := publish.WriteDir(out, map[string][]byte{
		"index.html": []byte("first"),
		"stale.html": []byte("stale"),
	}); err != nil {
		t.Fatalf("first write: %v", err)
	}

	if err := publish.WriteDir(out, map[string][]byte{
		"index.html": []byte("second"),
	}); err != nil {
		t.Fatalf("second write: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if string(body) != "second" {
		t.Errorf("index.html = %q, want %q", body, "second")
	}
	if _, err := os.Stat(filepath.Join(out, "stale.html")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a file from the previous build survived the replacement")
	}
}

func TestWriteDirLeavesThePreviousBuildOnAFailedWrite(t *testing.T) {
	out := filepath.Join(t.TempDir(), "dist")

	if err := publish.WriteDir(out, map[string][]byte{"index.html": []byte("previous")}); err != nil {
		t.Fatalf("seed the output: %v", err)
	}

	err := publish.WriteDir(out, map[string][]byte{
		"index.html":   []byte("replacement"),
		"../escape.go": []byte("not here"),
	})
	if err == nil {
		t.Fatal("WriteDir accepted a path outside the output directory")
	}

	body, readErr := os.ReadFile(filepath.Join(out, "index.html"))
	if readErr != nil {
		t.Fatalf("read the previous build: %v", readErr)
	}
	if string(body) != "previous" {
		t.Errorf("the previous build was replaced by a failed write: %q", body)
	}
}

func TestWriteDirRefusesToReplaceARegularFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "dist")
	if err := os.WriteFile(target, []byte("important"), 0o600); err != nil {
		t.Fatalf("seed the file: %v", err)
	}

	err := publish.WriteDir(target, map[string][]byte{"index.html": []byte("build")})
	if err == nil {
		t.Fatal("WriteDir replaced a regular file")
	}

	body, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read the file: %v", readErr)
	}
	if string(body) != "important" {
		t.Errorf("the file was modified: %q", body)
	}
}

func copyFiles(files map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for name, body := range files {
		out[name] = append([]byte(nil), body...)
	}
	return out
}
