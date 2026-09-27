package export

import (
	"context"
	"fmt"

	"github.com/divijg19/Verse/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pool is indirected through a variable so a test can point Build at a specific connection.
//
// The rest of the application reaches for database.Pool directly, and so does this by default. The
// indirection exists because an export is a bulk read of the entire body of work, which makes it the
// one operation most likely to be aimed somewhere other than the running service's own pool -- and
// that should be a deliberate choice at the call site rather than an accident.
var pool = func() *pgxpool.Pool { return database.Pool }

// UsePool directs Build at a specific pool for the duration of a test.
func UsePool(p *pgxpool.Pool) { pool = func() *pgxpool.Pool { return p } }

func versionsFor(ctx context.Context, poemID string) ([]Version, error) {
	p := pool()
	if p == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	rows, err := p.Query(ctx, `
        SELECT id, content, recorded_at
        FROM poem_versions
        WHERE poem_id = $1
        ORDER BY seq`, poemID)
	if err != nil {
		return nil, fmt.Errorf("read versions of %s: %w", poemID, err)
	}
	defer rows.Close()

	// Non-nil even when empty, so a poem with no history serializes as [] rather than null. A null
	// would make "no history" and "field absent" indistinguishable to a consumer, which is the kind
	// of ambiguity that only surfaces during a restore.
	out := []Version{}
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.ID, &v.Content, &v.RecordedAt); err != nil {
			return nil, fmt.Errorf("read version: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read versions of %s: %w", poemID, err)
	}
	return out, nil
}
