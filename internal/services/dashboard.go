package services

import (
	"context"
	"fmt"
	"time"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/models"
)

// TotalPoems returns the total number of non-deleted poems in the database.
func TotalPoems(ctx context.Context) (int, error) {
	if database.Pool == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	var count int
	row := database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM poems WHERE deleted_at IS NULL`)
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// CurrentStreak returns the number of consecutive days with poems counting backwards from today.
//
// A day is a UTC day, and both halves of the query say so. The SQL carries AT TIME ZONE 'UTC' because
// DATE() over a timestamptz resolves in the session's zone otherwise, and the consumer below formats
// with .UTC() and counts back from clock.TodayUTC(). The two halves have to agree on which day a poem
// belongs to, or a work written at 10:00 UTC lands on yesterday's square for a reader at +05:30 and a
// real streak reads as broken. The pool also pins every session to UTC, so the clause is redundancy
// with a purpose: it is the statement of intent for anyone reading the query, and the pin is the floor
// for a future query written without it.
func CurrentStreak(ctx context.Context) (int, error) {
	if database.Pool == nil {
		return 0, fmt.Errorf("database not initialized")
	}

	rows, err := database.Pool.Query(ctx, `
		SELECT DISTINCT DATE(created_at AT TIME ZONE 'UTC')
		FROM poems
		WHERE deleted_at IS NULL
		AND created_at >= NOW() - INTERVAL '365 days'
		ORDER BY 1 DESC`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	present := map[string]struct{}{}
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return 0, err
		}
		present[d.UTC().Format("2006-01-02")] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	streak := 0
	day := clock.TodayUTC()
	for {
		key := day.Format("2006-01-02")
		if _, ok := present[key]; !ok {
			break
		}
		streak++
		day = day.AddDate(0, 0, -1)
	}

	return streak, nil
}

// MonthActivity returns distinct active dates for the selected month.
func MonthActivity(ctx context.Context, month time.Time) ([]time.Time, error) {
	if database.Pool == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	monthStart := NormalizeMonth(month)
	monthEnd := monthStart.AddDate(0, 1, 0)

	rows, err := database.Pool.Query(ctx, `
		SELECT DISTINCT DATE(created_at AT TIME ZONE 'UTC')
		FROM poems
		WHERE deleted_at IS NULL
		AND created_at >= $1
		AND created_at < $2
		ORDER BY 1 ASC`, monthStart, monthEnd)
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
		dates = append(dates, d.UTC().Truncate(24*time.Hour))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return dates, nil
}

// LatestPoem returns the most recent non-deleted poem.
//
// The id tiebreak is not cosmetic here, and this is the one place it changes what is shown rather than
// just the order of a list. LIMIT 1 over a bare ORDER BY created_at DESC returns whichever tied row the
// planner reaches first, so two works sharing a timestamp would make "the most recent" an arbitrary
// choice that could differ between requests. now() is a transaction timestamp with microsecond
// resolution, so a tie is unlikely, and UpdatePoem-style bursts make it not merely theoretical.
func LatestPoem(ctx context.Context) (models.Poem, error) {
	var poem models.Poem
	if database.Pool == nil {
		return poem, fmt.Errorf("database not initialized")
	}

	row := database.Pool.QueryRow(ctx, `
		SELECT id, content, created_at
		FROM poems
		WHERE deleted_at IS NULL
		ORDER BY created_at DESC, id DESC
		LIMIT 1`)
	if err := row.Scan(&poem.ID, &poem.Content, &poem.CreatedAt); err != nil {
		return poem, err
	}

	return poem, nil
}

// NormalizeMonth returns the first day of the month in UTC.
func NormalizeMonth(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
}
