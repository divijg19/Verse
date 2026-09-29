package tests

import (
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/services"
)

// TestPagingArgumentsAreClampedByTheService covers the three paged reads, which take limit and
// offset from a caller and hand them to Postgres as LIMIT and OFFSET.
//
// Postgres is strict about these in a way that is easy to forget: OFFSET -1 is an error, not an
// empty result, so a bad argument becomes a 500. Each function therefore clamps. ListPoems did not,
// and the HTTP handler happened to clamp on its behalf, which meant the guarantee lived in the
// caller rather than in the function -- one more caller, or a test calling it directly, and a negative
// offset would surface as a database error.
//
// This asserts the behavior rather than the code, so a future refactor that drops a clamp fails
// here instead of in production.
func TestPagingArgumentsAreClampedByTheService(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	// Enough works that a page boundary exists to be argued about.
	now := time.Now().UTC()
	for i := 0; i < 5; i++ {
		insertPoemAt(t, "a work for paging", now.AddDate(0, 0, -i))
	}

	cases := []struct {
		name         string
		call         func(limit, offset int) (int, error)
		wantOnNegOff int
	}{
		{
			name: "ListPoems",
			call: func(limit, offset int) (int, error) {
				poems, err := services.ListPoems(t.Context(), limit, offset)
				return len(poems), err
			},
			wantOnNegOff: 5,
		},
		{
			name: "SearchPoems",
			call: func(limit, offset int) (int, error) {
				poems, err := services.SearchPoems(t.Context(), "paging", limit, offset)
				return len(poems), err
			},
			wantOnNegOff: 5,
		},
		{
			name: "ListDeletedPoems",
			call: func(limit, offset int) (int, error) {
				poems, err := services.ListDeletedPoems(t.Context(), limit, offset)
				return len(poems), err
			},
			// Nothing is in the recycle here, so the count is 0 either way. The assertion is that
			// this does not error, which is the whole point: without the clamp Postgres rejects it.
			wantOnNegOff: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A negative offset must read as page zero, not as a database error.
			got, err := tc.call(10, -1)
			if err != nil {
				t.Fatalf("(%d, -1) returned %v, want no error.\n"+
					"  Postgres rejects a negative OFFSET outright, so an unclamped value reaches the "+
					"database and comes back as a 500 rather than as the first page.", 10, err)
			}
			if got != tc.wantOnNegOff {
				t.Errorf("(%d, -1) returned %d works, want %d", 10, got, tc.wantOnNegOff)
			}

			// A zero or negative limit falls back to the default rather than returning nothing.
			// Without this the caller would see an empty library rather than an error, which is the
			// failure mode that is harder to notice.
			if got, err := tc.call(0, 0); err != nil || got != tc.wantOnNegOff {
				t.Errorf("(0, 0) returned %d works and error %v, want %d works and no error",
					got, err, tc.wantOnNegOff)
			}
			if got, err := tc.call(-5, 0); err != nil || got != tc.wantOnNegOff {
				t.Errorf("(-5, 0) returned %d works and error %v, want %d works and no error",
					got, err, tc.wantOnNegOff)
			}
		})
	}
}
