package templ

import "fmt"

// publicRailFor returns the navigation pill on one side of a page, and whether there is one.
//
// The two sides of a page are its neighbors in the ring. A work and the not-found page have one
// neighbor and one blank side, and a blank side renders nothing at all rather than an empty pill.
func publicRailFor(page PublicPage, side string) (PublicRail, bool) {
	left, right := publicRails(page)
	switch side {
	case "left":
		if left == nil {
			return PublicRail{}, false
		}
		return *left, true
	case "right":
		if right == nil {
			return PublicRail{}, false
		}
		return *right, true
	default:
		return PublicRail{}, false
	}
}

// railClass names a rail.
//
// The side is part of the class name rather than a separate attribute, because the arrow's
// direction and the rail's alignment both follow from it, and one class carries both.
func railClass(side string) string {
	switch side {
	case "left":
		return "public-rail public-rail--left"
	case "right":
		return "public-rail public-rail--right"
	default:
		return "public-rail"
	}
}

// railLabel names a rail for a screen reader.
//
// The visible label on the pill is the destination's name, which on its own tells a screen reader
// that there is a link called "Archive" and nothing about where it goes. These say which way round
// the ring the link lies.
func railLabel(side string) string {
	switch side {
	case "left":
		return "Previous page"
	case "right":
		return "Next page"
	default:
		return "Page navigation"
	}
}

// countLabel describes how many works the archive holds.
//
// It is written out rather than reduced to a numeral, because a numeral alone on a page tells a
// reader how many and not what they are counting.
func countLabel(count int) string {
	switch count {
	case 1:
		return "One published work"
	default:
		return fmt.Sprintf("%d published works", count)
	}
}
