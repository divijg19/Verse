package handlers

import (
	"testing"

	"github.com/divijg19/Verse/templ"
)

// TestLinkStatePrecedence pins the one server-side predicate behind every Share row label. The
// browser never infers state from an empty URL, so this matrix is the contract for the whole screen.
func TestLinkStatePrecedence(t *testing.T) {
	cases := []struct {
		name                        string
		revoked, expired, published bool
		recoverable                 bool
		want                        templ.LinkState
	}{
		{"active", false, false, true, true, templ.LinkStateActive},
		{"unrecoverable is not ended", false, false, true, false, templ.LinkStateUnrecoverable},
		{"expired", false, true, true, true, templ.LinkStateExpired},
		{"revoked", true, false, true, true, templ.LinkStateRevoked},
		{"revocation wins over expiry", true, true, true, true, templ.LinkStateRevoked},
		{"revocation wins over unpublish", true, false, false, true, templ.LinkStateRevoked},
		{"revocation wins over unrecoverable", true, false, true, false, templ.LinkStateRevoked},
		{"expiry wins over unrecoverable", false, true, true, false, templ.LinkStateExpired},
		{"unpublished", false, false, false, true, templ.LinkStateUnpublished},
		{"unpublished wins over unrecoverable", false, false, false, false, templ.LinkStateUnpublished},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := linkState(tc.revoked, tc.expired, tc.published, tc.recoverable)
			if got != tc.want {
				t.Errorf("linkState = %q, want %q", got, tc.want)
			}
			wantEnded := tc.want == templ.LinkStateExpired || tc.want == templ.LinkStateRevoked
			if got.Ended() != wantEnded {
				t.Errorf("%q.Ended() = %v, want %v", got, got.Ended(), wantEnded)
			}
		})
	}
}
