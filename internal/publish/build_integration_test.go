package publish_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/publish"
)

func TestBuildPublishesOnlyThePublishedLiveSet(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "build_set")
	seed(t, ctx, scratch,
		seedPoem{id: "aaaaaaaa-0000-0000-0000-000000000011", content: "A Published Work\n\npublic body", published: true},
		seedPoem{id: "aaaaaaaa-0000-0000-0000-000000000012", content: "A Draft Work\n\ndraft body"},
		seedPoem{id: "aaaaaaaa-0000-0000-0000-000000000013", content: "A Deleted Work\n\ndeleted body", published: true, deleted: true},
	)

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	out := filepath.Join(t.TempDir(), "dist")
	report, err := publish.Build(ctx, out, publish.Options{
		Title:       "Verse",
		Description: "A selection of published work.",
		BaseURL:     "https://verse.test",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if report.Works != 1 {
		t.Errorf("report says %d works, want 1", report.Works)
	}

	files := readBuild(t, out)

	for _, required := range []string{
		"index.html",
		"archive/index.html",
		"404.html",
		"search.json",
		"sitemap.xml",
		"feed.xml",
		"robots.txt",
		"w/aaaaaaaa-0000-0000-0000-000000000011/index.html",
	} {
		if _, ok := files[required]; !ok {
			t.Errorf("missing output %q", required)
		}
	}

	if _, ok := files["w/aaaaaaaa-0000-0000-0000-000000000012/index.html"]; ok {
		t.Error("a draft work got a public page")
	}
	if _, ok := files["w/aaaaaaaa-0000-0000-0000-000000000013/index.html"]; ok {
		t.Error("a published-then-deleted work got a public page")
	}

	archive := string(files["archive/index.html"])
	if !strings.Contains(archive, "A Published Work") {
		t.Error("the archive does not list the published work")
	}
	if strings.Contains(archive, "A Draft Work") || strings.Contains(archive, "A Deleted Work") {
		t.Error("the archive lists a work that is not published and live")
	}

	for name, body := range files {
		text := string(body)
		if strings.Contains(text, "draft body") {
			t.Errorf("%s contains draft content", name)
		}
		if strings.Contains(text, "deleted body") {
			t.Errorf("%s contains deleted content", name)
		}
		if strings.Contains(text, "aaaaaaaa-0000-0000-0000-000000000012") {
			t.Errorf("%s contains the draft's id", name)
		}
		if strings.Contains(text, "aaaaaaaa-0000-0000-0000-000000000013") {
			t.Errorf("%s contains the deleted work's id", name)
		}
	}

	search := string(files["search.json"])
	if !strings.Contains(search, "A Published Work") {
		t.Error("search.json does not list the published work")
	}
}

func TestBuildIsByteIdenticalAcrossRuns(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "build_determinism")
	seed(t, ctx, scratch,
		seedPoem{id: "bbbbbbbb-0000-0000-0000-000000000021", content: "First\n\nfirst body", published: true},
		seedPoem{id: "bbbbbbbb-0000-0000-0000-000000000022", content: "Second\n\nsecond body", published: true},
	)

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	options := publish.Options{
		Title:       "Verse",
		Description: "A selection of published work.",
		BaseURL:     "https://verse.test",
	}

	first := filepath.Join(t.TempDir(), "dist-a")
	second := filepath.Join(t.TempDir(), "dist-b")
	if _, err := publish.Build(ctx, first, options); err != nil {
		t.Fatalf("first build: %v", err)
	}
	if _, err := publish.Build(ctx, second, options); err != nil {
		t.Fatalf("second build: %v", err)
	}

	a := readBuild(t, first)
	b := readBuild(t, second)
	if len(a) != len(b) {
		t.Fatalf("builds have different file counts: %d and %d", len(a), len(b))
	}
	for name, body := range a {
		other, ok := b[name]
		if !ok {
			t.Errorf("second build is missing %q", name)
			continue
		}
		if string(body) != string(other) {
			t.Errorf("%q differs between builds", name)
		}
	}
}

func TestBuildOnAnEmptyLibrary(t *testing.T) {
	dsn := requireDSN(t)
	ctx := context.Background()

	scratch := newScratch(t, dsn, "build_empty")

	previous := database.Pool
	database.Pool = scratch
	defer func() { database.Pool = previous }()

	out := filepath.Join(t.TempDir(), "dist")
	if _, err := publish.Build(ctx, out, publish.Options{}); err != nil {
		t.Fatalf("build: %v", err)
	}

	files := readBuild(t, out)
	if !strings.Contains(string(files["index.html"]), "Nothing published yet.") {
		t.Error("the empty index does not say there is nothing published")
	}
	if !strings.Contains(string(files["archive/index.html"]), "Nothing published yet.") {
		t.Error("the empty archive does not say there is nothing published")
	}
	if !strings.Contains(string(files["search.json"]), `"entries": []`) {
		t.Errorf("the empty search index is %s", files["search.json"])
	}
}

func readBuild(t *testing.T, root string) map[string][]byte {
	t.Helper()

	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = body
		return nil
	})
	if err != nil {
		t.Fatalf("read the build at %s: %v", root, err)
	}
	return files
}
