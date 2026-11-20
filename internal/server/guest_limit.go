package server

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/divijg19/Verse/internal/clock"
	"github.com/divijg19/Verse/internal/database"
)

// guestRequestLimit bounds anonymous traffic per address and route family. It is deliberately
// generous for the editor's autosave and tight for entry/submit, where each request is a new
// capability check. The counter is shared by every instance through the database.
const (
	guestEntryLimit  = 60
	guestEditorLimit = 600
	guestWindow      = 15 * time.Minute
)

// guestRateLimit returns middleware keyed on the caller's address and a route family. Fail-open
// matches the login limiter: a database outage must not make the whole guest surface unavailable.
func guestRateLimit(cfg *authConfig, kind string, limit int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if database.Pool != nil {
				subject := append([]byte(kind+"|"), subjectKey(r, cfg.secret)...)
				now := clock.NowUTC()
				var count int
				err := database.Pool.QueryRow(r.Context(), `
                    INSERT INTO guest_requests (subject, kind, window_start, count)
                    VALUES ($1, $2, $3, 1)
                    ON CONFLICT (subject, kind) DO UPDATE SET
                        count = CASE WHEN guest_requests.window_start < $4 THEN 1
                                     ELSE guest_requests.count + 1 END,
                        window_start = CASE WHEN guest_requests.window_start < $4 THEN $3
                                            ELSE guest_requests.window_start END
                    RETURNING count`, subject, kind, now, now.Add(-guestWindow)).Scan(&count)
				if err == nil && count > limit {
					w.Header().Set("Retry-After", "900")
					http.Error(w, "too many requests", http.StatusTooManyRequests)
					return
				}
				if err != nil && !errorsIsNoRows(err) {
					log.Printf("guest rate limit unavailable: %v", err)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func errorsIsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
