package tests

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/services"
)

func TestPoemViewRouteRendersPoem(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	content := "The river forgets\nNight keeps the names"
	id := insertPoem(t, content)

	srv := newTestServer(t)
	defer srv.Close()

	status, body, _ := get(t, srv.URL+"/poem/"+id, nil)
	if status != 200 {
		t.Fatalf("GET /poem/{id} status = %d, want 200", status)
	}
	if !strings.Contains(body, "Night keeps the names") {
		t.Fatalf("poem view missing poem content: %q", body)
	}
	if !strings.Contains(body, `hx-get="/library"`) {
		t.Fatalf("poem view missing back link to /library")
	}
	if !strings.Contains(body, `hx-get="/editor/`+id+`"`) {
		t.Fatalf("poem view missing link to /editor/{id}")
	}
	if !strings.Contains(body, `class="verse-button-danger"`) {
		t.Fatalf("poem view missing shared danger button styling: %q", body)
	}
}

// TestAPoemFetchFaultIsNotReportedAsAMissingPoem is the reason writePoemFetchError exists.
//
// Both handlers reported every error as 404, which is a claim about the work rather than about the
// service. During a Neon outage the author saw their library as empty, or a poem as deleted, with
// nothing logged. A 404 says "this does not exist"; the service does not get to say that when it
// could not ask.
func TestAPoemFetchFaultIsNotReportedAsAMissingPoem(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	// Seeding proves the work exists, so a 404 for it is unambiguously wrong.
	id := insertPoem(t, "a work that exists")

	srv := newTestServer(t)

	// Close the pool the request will use. This is the shape of a real fault -- unreachable
	// database -- rather than a fabricated error value, so it exercises the same path an outage
	// would.
	live := database.Pool
	database.Pool = nil
	t.Cleanup(func() { database.Pool = live })

	for _, path := range []string{"/poem/" + id, "/editor/" + id} {
		t.Run(path, func(t *testing.T) {
			status, body, _ := get(t, srv.URL+path, nil)
			if status != http.StatusInternalServerError {
				t.Fatalf("GET %s with the database unreachable = %d, want 500.\n"+
					"  body: %s\n"+
					"  A 404 here asserts the work does not exist, which is a claim the service "+
					"cannot support while the database is unreachable.",
					path, status, truncate([]byte(body)))
			}
			if strings.Contains(body, "database") {
				t.Errorf("the 500 body disclosed the fault: %q", body)
			}
		})
	}
}

// TestAMissingPoemIsStillAMiss keeps the fix honest in the other direction: distinguishing the two
// cases must not turn every miss into a 500.
func TestAMissingPoemIsStillAMiss(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)

	for _, path := range []string{
		"/poem/00000000-0000-0000-0000-000000000000",
		"/editor/00000000-0000-0000-0000-000000000000",
	} {
		t.Run(path, func(t *testing.T) {
			status, _, _ := get(t, srv.URL+path, nil)
			if status != http.StatusNotFound {
				t.Errorf("GET %s for an id that does not exist = %d, want 404", path, status)
			}
		})
	}
}

// TestADeletedPoemIsStillAMiss: a soft-deleted work is filtered by GetPoem, so it is a miss. Telling
// the author their work is gone is the same answer as for an id that never existed, and the recycle
// is where a restore comes from.
func TestADeletedPoemIsStillAMiss(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	id := insertPoem(t, "deleted, not gone")
	if err := services.SoftDeletePoem(context.Background(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}

	srv := newTestServer(t)

	status, _, _ := get(t, srv.URL+"/poem/"+id, nil)
	if status != http.StatusNotFound {
		t.Errorf("GET a soft-deleted poem = %d, want 404", status)
	}
}

// TestAMalformedPoemIDIsABadRequest covers the other half of Phase 0.3.
//
// /poem/{id} and /editor/{id} passed the raw route parameter straight to the query, while the other
// two id-taking handlers validated it as a UUID. The queries are parameterised so this is not
// injectable, but it leaves the URL space unvalidated and the two pairs of handlers disagreeing about
// what an id is.
func TestAMalformedPoemIDIsABadRequest(t *testing.T) {
	connectTestDB(t)
	truncatePoems(t)

	srv := newTestServer(t)

	for _, path := range []string{"/poem/not-a-uuid", "/editor/not-a-uuid", "/poem/12345"} {
		t.Run(path, func(t *testing.T) {
			status, _, _ := get(t, srv.URL+path, nil)
			if status != http.StatusBadRequest {
				t.Errorf("GET %s = %d, want 400; the id should be rejected before it reaches the query",
					path, status)
			}
		})
	}
}
