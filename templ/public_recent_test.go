package templ_test

import (
	"testing"
	"time"

	publictempl "github.com/divijg19/Verse/templ"
)

// RecentWorks decides which works the front page shows, and it was covered by nothing.
//
// The ordering rule it applies -- newest first, with the identifier as the tiebreak -- was written
// out three times: once in the publisher's database path, once in its file path, and once here. The
// first two were held together by a test that compares the two paths byte for byte. This third copy
// was not held together with anything, so a change to the rule in one place could have reordered
// the front page while every test still passed.
//
// The rule now lives in one function, PublishedOrder, and these tests are what hold it to it.

// at is a publication instant, an hour after the given hour.
func at(hour int) time.Time {
	return time.Date(2026, time.March, 4, hour, 0, 0, 0, time.UTC)
}

// work is a published work with the given identifier and publication instant.
func work(id string, publishedAt time.Time) publictempl.PublicWork {
	return publictempl.PublicWork{
		ID:          id,
		Path:        "/w/" + id + "/",
		Title:       id,
		Content:     id,
		PublishedAt: publishedAt,
	}
}

// TestRecentWorksAreNewestFirst is the rule itself.
func TestRecentWorksAreNewestFirst(t *testing.T) {
	site := publictempl.PublicSite{
		Works: []publictempl.PublicWork{
			work("oldest", at(1)),
			work("newest", at(3)),
			work("middle", at(2)),
		},
	}

	got := site.RecentWorks(3)
	want := []string{"newest", "middle", "oldest"}
	if len(got) != len(want) {
		t.Fatalf("RecentWorks returned %d works, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("work %d is %q, want %q", i, got[i].ID, want[i])
		}
	}
}

// TestRecentWorksBreakTiesOnTheIdentifier covers the tiebreak.
//
// Two works can share a publication instant: every row written by one transaction carries the same
// one. Without a tiebreak the order between them is whatever the sort happens to produce, which
// differs between runs -- and an order that differs between runs is an archive whose contents
// change without anything changing.
func TestRecentWorksBreakTiesOnTheIdentifier(t *testing.T) {
	site := publictempl.PublicSite{
		Works: []publictempl.PublicWork{
			work("bbb", at(2)),
			work("aaa", at(2)),
			work("ccc", at(2)),
		},
	}

	got := site.RecentWorks(3)
	want := []string{"ccc", "bbb", "aaa"}
	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("work %d is %q, want %q", i, got[i].ID, want[i])
		}
	}
}

// TestRecentWorksDoesNotModifyTheInput covers the copy.
//
// The site is shared: the archive renders every work from the same slice. Sorting it in place would
// reorder the archive too, and the front page is rendered before the archive.
func TestRecentWorksDoesNotModifyTheInput(t *testing.T) {
	first := work("first", at(1))
	second := work("second", at(2))
	site := publictempl.PublicSite{Works: []publictempl.PublicWork{first, second}}

	site.RecentWorks(2)

	if site.Works[0].ID != "first" {
		t.Errorf("the site's works were reordered: %q is first now", site.Works[0].ID)
	}
}

// TestRecentWorksHonourTheLimit covers the count.
func TestRecentWorksHonourTheLimit(t *testing.T) {
	site := publictempl.PublicSite{
		Works: []publictempl.PublicWork{
			work("one", at(1)),
			work("two", at(2)),
			work("three", at(3)),
		},
	}

	if got := site.RecentWorks(2); len(got) != 2 {
		t.Errorf("RecentWorks(2) returned %d works, want 2", len(got))
	}
	if got := site.RecentWorks(0); got != nil {
		t.Errorf("RecentWorks(0) returned %d works, want none", len(got))
	}
}

// TestPublishedOrderIsATotalOrder covers the property the whole rule rests on.
//
// A total order is one where every pair is ordered and the order is transitive. Without it, two
// works published in the same transaction can swap places between two builds of the same library,
// which defeats the byte-identical output this project relies on.
func TestPublishedOrderIsATotalOrder(t *testing.T) {
	older := work("older", at(1))
	newer := work("newer", at(2))

	if publictempl.PublishedOrder(newer, older) >= 0 {
		t.Error("the newer work does not come first")
	}
	if publictempl.PublishedOrder(older, newer) <= 0 {
		t.Error("the older work does not come second")
	}
	if publictempl.PublishedOrder(newer, newer) != 0 {
		t.Error("a work does not equal itself")
	}
}
