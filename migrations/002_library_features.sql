-- Migration: add deleted_at for soft deletes
ALTER TABLE poems
ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP NULL;
