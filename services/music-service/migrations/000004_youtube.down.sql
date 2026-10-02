ALTER TABLE music.tracks DROP COLUMN IF EXISTS youtube_video_id;
ALTER TABLE music.albums DROP COLUMN IF EXISTS youtube_browse_id;
ALTER TABLE music.artists DROP COLUMN IF EXISTS youtube_browse_id;
