package services

import (
	"context"
	"fmt"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/models"
)

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

// UpdatePoem updates the content of an existing poem.
func UpdatePoem(ctx context.Context, id string, content string) error {
	if database.Pool == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := database.Pool.Exec(ctx, `UPDATE poems SET content = $1 WHERE id = $2`, content, id)
	return err
}

// SoftDeletePoem marks a poem as deleted by setting deleted_at.
func SoftDeletePoem(ctx context.Context, id string) error {
	if database.Pool == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := database.Pool.Exec(ctx, `UPDATE poems SET deleted_at = now() WHERE id = $1`, id)
	return err
}
