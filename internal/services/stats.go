package services

import (
    "context"
    "fmt"
    "sort"
    "time"

    "github.com/divijg19/Verse/internal/database"
)

// TotalPoems returns the total number of poems in the database.
func TotalPoems(ctx context.Context) (int, error) {
    if database.Pool == nil {
        return 0, fmt.Errorf("database not initialized")
    }

    var count int
    row := database.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM poems;")
    if err := row.Scan(&count); err != nil {
        return 0, err
    }
    return count, nil
}

// ActivityLast30Days returns the distinct dates in the last 30 days where a poem was created.
func ActivityLast30Days(ctx context.Context) ([]time.Time, error) {
    if database.Pool == nil {
        return nil, fmt.Errorf("database not initialized")
    }

    rows, err := database.Pool.Query(ctx, "SELECT DISTINCT DATE(created_at) FROM poems WHERE created_at >= NOW() - INTERVAL '30 days'")
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

    return dates, nil
}

// CurrentStreak returns the number of consecutive days with poems counting backwards from today.
func CurrentStreak(ctx context.Context) (int, error) {
    if database.Pool == nil {
        return 0, fmt.Errorf("database not initialized")
    }

    // Fetch distinct dates for the past year (safe window for streaks)
    rows, err := database.Pool.Query(ctx, "SELECT DISTINCT DATE(created_at) FROM poems WHERE created_at >= NOW() - INTERVAL '365 days' ORDER BY 1 DESC")
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

    streak := 0
    day := time.Now().UTC().Truncate(24 * time.Hour)
    for {
        key := day.Format("2006-01-02")
        if _, ok := present[key]; ok {
            streak++
            day = day.AddDate(0, 0, -1)
            continue
        }
        break
    }

    return streak, nil
}

// LongestStreak computes the maximum consecutive-day streak in the dataset.
func LongestStreak(ctx context.Context) (int, error) {
    if database.Pool == nil {
        return 0, fmt.Errorf("database not initialized")
    }

    rows, err := database.Pool.Query(ctx, "SELECT DISTINCT DATE(created_at) FROM poems ORDER BY 1 ASC")
    if err != nil {
        return 0, err
    }
    defer rows.Close()

    var dates []time.Time
    for rows.Next() {
        var d time.Time
        if err := rows.Scan(&d); err != nil {
            return 0, err
        }
        dates = append(dates, d.UTC().Truncate(24*time.Hour))
    }

    if len(dates) == 0 {
        return 0, nil
    }

    // Ensure ascending order
    sort.Slice(dates, func(i, j int) bool { return dates[i].Before(dates[j]) })

    longest := 1
    current := 1
    for i := 1; i < len(dates); i++ {
        if dates[i].Equal(dates[i-1].AddDate(0, 0, 1)) {
            current++
            if current > longest {
                longest = current
            }
        } else if dates[i].Equal(dates[i-1]) {
            // same day duplicate, ignore
            continue
        } else {
            current = 1
        }
    }

    return longest, nil
}
