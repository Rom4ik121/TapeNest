-- Per-user library on top of shared media (dedup stays on download.media).
-- display_title is the owner's rename; deleted_at hides a job without
-- removing a file another user may still share. poster_key is a jpeg frame
-- in the same bucket (empty until ffmpeg extracts one).
ALTER TABLE download.jobs
    ADD COLUMN display_title text NOT NULL DEFAULT '',
    ADD COLUMN deleted_at    timestamptz;

ALTER TABLE download.media
    ADD COLUMN poster_key text NOT NULL DEFAULT '';

CREATE INDEX jobs_user_visible_idx ON download.jobs (user_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
