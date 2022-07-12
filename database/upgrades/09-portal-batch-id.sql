-- v9: Add backfill batch ID for portals
ALTER TABLE portal ADD COLUMN batch_id TEXT DEFAULT '';
ALTER TABLE portal ADD COLUMN insertion_id TEXT DEFAULT '';
ALTER TABLE portal ADD COLUMN has_more_history BOOLEAN NOT NULL DEFAULT true;
-- only: postgres for next 3 lines
ALTER TABLE portal ALTER COLUMN batch_id DROP DEFAULT;
ALTER TABLE portal ALTER COLUMN insertion_id DROP DEFAULT;
ALTER TABLE portal ALTER COLUMN has_more_history DROP DEFAULT;
