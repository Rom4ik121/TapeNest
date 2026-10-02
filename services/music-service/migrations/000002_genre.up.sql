-- Stage "reco": genre/year per track (Navidrome ID3 tags) for the recommendation
-- service's taste profile; internal catalog export is keyset-paged by id.
ALTER TABLE music.tracks ADD COLUMN genre text NOT NULL DEFAULT '';
ALTER TABLE music.tracks ADD COLUMN year integer;
