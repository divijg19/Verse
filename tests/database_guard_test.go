package tests

import (
	"errors"
	"testing"
	"time"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/export"
	"github.com/divijg19/Verse/internal/services"
)

// TestEveryQueryPathFailsCleanlyWithoutAPool is the test for database.Require.
//
// The nil pool is reachable in a process that is running rather than merely misconfigured: /health is
// registered before Connect has finished, and the pool-footprint test replaces the global to count
// acquisitions. Every query path therefore has to survive a nil Pool. Dereferencing it panics, and a
// panic in a health handler takes the process down, which is the opposite of what a not-yet-ready
// check should do.
//
// One function per exported query path, because the guard is a line written eighteen times and this
// is what holds those eighteen in place. If a new query path is added without one, this fails only
// if it is listed here -- so the list is the specification, and a new path that panics will be found
// by the panic itself rather than by this test.
func TestEveryQueryPathFailsCleanlyWithoutAPool(t *testing.T) {
	previous := database.Pool
	database.Pool = nil
	t.Cleanup(func() { database.Pool = previous })

	if err := database.Require(); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("Require() = %v, want ErrNotInitialized", err)
	}
	if err := database.Ping(t.Context()); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("Ping() = %v, want ErrNotInitialized", err)
	}
	if err := database.RequireSchema(t.Context()); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("RequireSchema() = %v, want ErrNotInitialized", err)
	}

	if _, err := services.ListPoems(t.Context(), 10, 0); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("ListPoems() = %v, want ErrNotInitialized", err)
	}
	if _, err := services.SearchPoems(t.Context(), "anything", 10, 0); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("SearchPoems() = %v, want ErrNotInitialized", err)
	}
	if _, err := services.GetPoem(t.Context(), "id"); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("GetPoem() = %v, want ErrNotInitialized", err)
	}
	if _, err := services.GetPoemIncludingDeleted(t.Context(), "id"); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("GetPoemIncludingDeleted() = %v, want ErrNotInitialized", err)
	}
	if _, err := services.CreatePoem(t.Context(), "text"); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("CreatePoem() = %v, want ErrNotInitialized", err)
	}
	if err := services.UpdatePoem(t.Context(), "id", "text"); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("UpdatePoem() = %v, want ErrNotInitialized", err)
	}
	if err := services.SoftDeletePoem(t.Context(), "id"); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("SoftDeletePoem() = %v, want ErrNotInitialized", err)
	}
	if err := services.RestorePoem(t.Context(), "id"); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("RestorePoem() = %v, want ErrNotInitialized", err)
	}
	if _, err := services.ListDeletedPoems(t.Context(), 10, 0); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("ListDeletedPoems() = %v, want ErrNotInitialized", err)
	}
	if _, err := services.ListPoemVersions(t.Context(), "id"); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("ListPoemVersions() = %v, want ErrNotInitialized", err)
	}
	if _, err := services.GetPoemVersion(t.Context(), "id", "vid"); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("GetPoemVersion() = %v, want ErrNotInitialized", err)
	}
	if err := services.RestorePoemVersion(t.Context(), "id", "vid"); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("RestorePoemVersion() = %v, want ErrNotInitialized", err)
	}
	if _, err := services.CountDeletedPoems(t.Context()); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("CountDeletedPoems() = %v, want ErrNotInitialized", err)
	}
	if _, err := services.DashboardSummary(t.Context()); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("DashboardSummary() = %v, want ErrNotInitialized", err)
	}
	if _, err := services.ActivityDays(t.Context(), time.Now()); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("ActivityDays() = %v, want ErrNotInitialized", err)
	}
	if _, err := export.Build(t.Context(), true); !errors.Is(err, database.ErrNotInitialized) {
		t.Errorf("export.Build() = %v, want ErrNotInitialized", err)
	}
}
