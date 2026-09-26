package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/models"
	"github.com/google/uuid"
)

// ErrNotFound reports that a mutation matched no row.
//
// Previously UpdatePoem and SoftDeletePoem discarded the affected-row count, so a request naming a
// nonexistent id returned success. Callers need to distinguish "changed" from "no such thing".
var ErrNotFound = errors.New("not found")

// CreatePoem inserts a new poem and returns its id.
func CreatePoem(ctx context.Context, content string) (string, error) {
	if database.Pool == nil {
		return "", fmt.Errorf("database not initialized")
	}

	id := uuid.NewString()
	if _, err := database.Pool.Exec(ctx, `INSERT INTO poems (id, content) VALUES ($1, $2)`, id, content); err != nil {
		return "", err
	}

	return id, nil
}

// ListPoems returns the most recent poems (non-deleted) with limit/offset.
func ListPoems(ctx context.Context, limit, offset int) ([]models.Poem, error) {
	if database.Pool == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := database.Pool.Query(ctx, `
        SELECT id, content, created_at
        FROM poems
        WHERE deleted_at IS NULL
        ORDER BY created_at DESC
        LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Poem
	for rows.Next() {
		var p models.Poem
		if err := rows.Scan(&p.ID, &p.Content, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SearchPoems returns poems matching q (ILIKE), limited with optional offset.
func SearchPoems(ctx context.Context, q string, limit int, offset int) ([]models.Poem, error) {
	if database.Pool == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := database.Pool.Query(ctx, `
        SELECT id, content, created_at
        FROM poems
        WHERE deleted_at IS NULL
        AND content ILIKE '%' || $1 || '%'
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3`, q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Poem
	for rows.Next() {
		var p models.Poem
		if err := rows.Scan(&p.ID, &p.Content, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetPoem returns a single poem by id if not deleted.
func GetPoem(ctx context.Context, id string) (models.Poem, error) {
	var p models.Poem
	if database.Pool == nil {
		return p, fmt.Errorf("database not initialized")
	}
	row := database.Pool.QueryRow(ctx, `
        SELECT id, content, created_at
        FROM poems
        WHERE id = $1
        AND deleted_at IS NULL`, id)
	if err := row.Scan(&p.ID, &p.Content, &p.CreatedAt); err != nil {
		return p, err
	}
	return p, nil
}

// UpdatePoem updates the content of an existing active poem.
//
// The deleted_at guard matters: without it a soft-deleted work could be silently edited, and the
// edit would appear to succeed while remaining hidden.
func UpdatePoem(ctx context.Context, id string, content string) error {
	if database.Pool == nil {
		return fmt.Errorf("database not initialized")
	}

	tag, err := database.Pool.Exec(ctx,
		`UPDATE poems SET content = $1 WHERE id = $2 AND deleted_at IS NULL`, content, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDeletePoem marks an active poem as deleted by setting deleted_at.
//
// Already-deleted rows report ErrNotFound rather than succeeding silently, so a repeated delete is
// visible to the caller instead of looking like a fresh success.
func SoftDeletePoem(ctx context.Context, id string) error {
	if database.Pool == nil {
		return fmt.Errorf("database not initialized")
	}

	tag, err := database.Pool.Exec(ctx,
		`UPDATE poems SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
