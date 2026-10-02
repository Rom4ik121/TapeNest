-- YouTube Music placeholders (ADR 0012). A track found on YouTube Music gets a
-- stable row so likes and playlists work without a file on disk. Playback
-- streams the audio; these columns do not store a downloaded copy.
ALTER TABLE music.tracks ADD COLUMN youtube_video_id text UNIQUE;
ALTER TABLE music.albums ADD COLUMN youtube_browse_id text UNIQUE;
ALTER TABLE music.artists ADD COLUMN youtube_browse_id text UNIQUE;
