-- Stage "acquisition" (ADR 0011): the catalog also holds *remote* items that
-- are known from MusicBrainz but not on the server yet (navidrome_id IS NULL).
-- They get their final UUIDs immediately, so likes / playlists / wave keep
-- working unchanged once the file is acquired and Navidrome's sync claims them.
ALTER TABLE music.artists ALTER COLUMN navidrome_id DROP NOT NULL;
ALTER TABLE music.artists ADD COLUMN mbid uuid UNIQUE;

ALTER TABLE music.albums ALTER COLUMN navidrome_id DROP NOT NULL;
ALTER TABLE music.albums ADD COLUMN mbid uuid UNIQUE; -- MusicBrainz release group

ALTER TABLE music.tracks ALTER COLUMN navidrome_id DROP NOT NULL;
ALTER TABLE music.tracks ADD COLUMN mb_recording_id uuid UNIQUE;
ALTER TABLE music.tracks ADD COLUMN mb_release_group_id uuid;
ALTER TABLE music.tracks ADD COLUMN mb_artist_id uuid;
CREATE INDEX tracks_album_order ON music.tracks (album_id, track_no) WHERE deleted_at IS NULL;
CREATE INDEX albums_artist ON music.albums (artist_id);

-- files imported by acquisition-service: exact size (hardlink of the torrent
-- file) lets the Navidrome sync claim the placeholder even when tags differ
CREATE TABLE music.acquired_files (
    path            text PRIMARY KEY,
    mb_recording_id uuid NOT NULL,
    mb_release_group_id uuid,
    size_bytes      bigint NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX acquired_files_size ON music.acquired_files (size_bytes);
