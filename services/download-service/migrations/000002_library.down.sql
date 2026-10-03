DROP INDEX IF EXISTS download.jobs_user_visible_idx;
ALTER TABLE download.media DROP COLUMN IF EXISTS poster_key;
ALTER TABLE download.jobs DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE download.jobs DROP COLUMN IF EXISTS display_title;
