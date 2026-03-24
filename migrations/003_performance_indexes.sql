-- Migration: add index support for active poem timelines and latest-poem lookups
CREATE INDEX IF NOT EXISTS idx_poems_active_created_at
ON poems (created_at DESC)
WHERE deleted_at IS NULL;
