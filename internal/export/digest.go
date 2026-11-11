package export

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"time"
)

// PublishedDigest is a digest of the published set.
//
// It answers one question without a database: has anything a reader can see changed? Two exports of
// a library whose drafts and deleted works differ have the same digest, because neither of them is
// visible.
//
// The export's own creation instant is not part of it, for the same reason. A digest that changed
// every time the export ran would answer "has anything changed" with "yes" every time, and the
// deploy step that consults it would rebuild and redeploy the site on every run.
//
// The digest covers the published works' identifiers, publication instants and content. The
// publication instant is in it because a work republished under a new date is a different work as
// far as a reader's feed and sitemap are concerned.
//
// The order is the identifier's rather than the file's. The file's order comes from a query's ORDER
// BY, and a digest that changed because a query changed its ordering would be a digest that reports
// a change where there is none.
func PublishedDigest(poems []Poem) string {
	published := make([]Poem, 0, len(poems))
	for _, poem := range poems {
		if poem.PublishedAt == nil || poem.DeletedAt != nil {
			continue
		}
		published = append(published, poem)
	}

	slices.SortFunc(published, func(a, b Poem) int { return cmp.Compare(a.ID, b.ID) })

	digest := sha256.New()
	for _, poem := range published {
		publishedAt := poem.PublishedAt.UTC().Format(time.RFC3339Nano)
		// The separators are NUL, which cannot occur in any of the three fields, so no two
		// different sets of works can produce the same digest by rearrangement.
		//
		// The error is checked because a hash that failed to accept its input would produce a
		// digest of nothing, and a digest of nothing is a value that compares equal to another
		// digest of nothing. It cannot happen -- sha256's writer never fails -- but the check
		// costs one line and the alternative is a digest that is quietly wrong.
		if _, err := fmt.Fprintf(digest, "%s\x00%s\x00%s\x00", poem.ID, publishedAt, poem.Content); err != nil {
			return ""
		}
	}

	return hex.EncodeToString(digest.Sum(nil))
}
