package models

import "time"

// Poem is the DB model used by services.
type Poem struct {
	ID        string
	Content   string
	CreatedAt time.Time
}
