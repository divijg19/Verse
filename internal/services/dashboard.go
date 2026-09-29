package services

import (
	"context"
	"time"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/models"
)

// NormalizeMonth returns the first day of the month in UTC.
func NormalizeMonth(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// ActivityDays returns every UTC day on which a live work was created, from the earlier of the streak
// window and the start of month, up to now.
//
// One query where there were two. CurrentStreak and MonthActivity were the same statement with
// different bounds -- both were SELECT DISTINCT DATE(created_at AT TIME ZONE 'UTC') over the same
// predicate -- and the dashboard ran them concurrently, so a single page load took two of a five-
// connection pool to answer a question about days.
//
// The lower bound is a LEAST rather than either window alone, and that is the detail that makes
// merging them correct. The heatmap is paged: the month arrows reach a month the 365-day streak window
// does not cover, and a query bounded only by the streak would report an empty month for work that
// exists. Taking the earlier of the two starts gives a superset of both windows, and the caller
// narrows to whichever it needs.
//
// The result is at most a few thousand short values even for an old month, so the whole window is
// returned and the bucketing happens in Go. That is a deliberate trade: a little more transferred to
// save a round trip and a pool connection, on a table holding one author's work.
func ActivityDays(ctx context.Context, month time.Time) ([]time.Time, error) {
	if err := database.Require(); err != nil {
		return nil, err
	}

	rows, err := database.Pool.Query(ctx, `
		SELECT DISTINCT DATE(created_at AT TIME ZONE 'UTC')
		FROM poems
		WHERE deleted_at IS NULL
		AND created_at >= LEAST(NOW() - INTERVAL '365 days', $1::timestamptz)
		ORDER BY 1 ASC`, NormalizeMonth(month))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dates []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		dates = append(dates, d.UTC())
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dates, nil
}

// StreakFromDays counts consecutive active days backwards from today.
//
// A pure function over the days ActivityDays returned, so the arithmetic is testable without a
// database and so the streak and the heatmap are known to agree about which day a work belongs to.
// Both read the same set, which is the property that matters: a streak and a heatmap computed from
// two different queries can disagree, and did once, when one bucketed on the session's zone and the
// other did not.
//
// The day arithmetic is AddDate rather than subtracting a fixed duration, so the count stays correct
// across a month boundary, a year boundary, and a leap day -- none of which a 24*time.Hour step would
// be.
func StreakFromDays(days []time.Time, today time.Time) int {
	present := make(map[string]struct{}, len(days))
	for _, d := range days {
		present[d.UTC().Format(dayLayout)] = struct{}{}
	}

	streak := 0
	day := today.UTC()
	for {
		if _, ok := present[day.Format(dayLayout)]; !ok {
			break
		}
		streak++
		day = day.AddDate(0, 0, -1)
	}
	return streak
}

// dayLayout is how a UTC day is spelled in the keys above and in the comparisons between them.
const dayLayout = "2006-01-02"

// MonthDays narrows ActivityDays' result to the given month.
//
// The caller passes a month the query already covered, so this is a filter over values in hand rather
// than another round trip. NormalizeMonth is applied to both sides so a month passed as its fifteenth
// behaves the same as one passed as its first.
func MonthDays(days []time.Time, month time.Time) []time.Time {
	monthStart := NormalizeMonth(month)
	next := monthStart.AddDate(0, 1, 0)

	var out []time.Time
	for _, d := range days {
		utc := d.UTC()
		if !utc.Before(monthStart) && utc.Before(next) {
			out = append(out, utc)
		}
	}
	return out
}

// Summary is the pair of dashboard figures that do not come from ActivityDays.
type Summary struct {
	// Total is the number of live works.
	Total int
	// Latest is the most recent live work, or nil when there are none.
	Latest *models.Poem
}

// DashboardSummary returns the total and the most recent work in one statement.
//
// The two used to be separate queries run concurrently, which was two of five connections. They are
// unrelated aggregates -- a count and a single row -- so a derived table joined to a one-row anchor
// returns both in one round trip. The anchor is what makes it a left join: with no live work the
// latest-row subquery yields nothing, and the count is still owed a row to live in.
//
// Written this way rather than as two statements on one connection because the extended protocol
// permits one statement per execution, and reaching for the simple protocol to batch them would hand
// up multi-statement support to a query that does not need it.
func DashboardSummary(ctx context.Context) (Summary, error) {
	var summary Summary
	if err := database.Require(); err != nil {
		return summary, err
	}

	row := database.Pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*)::int FROM poems WHERE deleted_at IS NULL),
			latest.id,
			latest.content,
			latest.created_at
		FROM (SELECT 1) AS anchor
		LEFT JOIN (
			SELECT `+models.PoemColumns+`
			FROM poems
			WHERE deleted_at IS NULL
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		) AS latest ON true`)

	var id, content *string
	var createdAt *time.Time
	if err := row.Scan(&summary.Total, &id, &content, &createdAt); err != nil {
		return Summary{}, err
	}
	if id != nil && content != nil && createdAt != nil {
		summary.Latest = &models.Poem{ID: *id, Content: *content, CreatedAt: *createdAt}
	}
	return summary, nil
}
