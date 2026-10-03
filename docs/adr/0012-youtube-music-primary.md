# ADR 0012: YouTube Music is the primary catalog

- Status: Accepted, amended by [0014](0014-drop-cinema-and-torrent-stack.md) (torrent fallback removed; `MUSIC_SOURCES` default is `youtube`)
- Date: 2026-10-02

## Context

WavePlayer search used to see the local Navidrome library, then MusicBrainz placeholders that acquisition-service fills by torrent (ADR 0011). That is a poor fit for "search any track and press play": torrents are slow, need indexers the operator must add, and tests must not download copyrighted albums.

The user asked for the same behavior as an unofficial YouTube Music client: search YouTube Music and play the audio. Metrolist and InnerTune implement that, but both are GPL-3.0. Their source is not copied into this repository.

## Decisions

### 1. YouTube Music, plus the local library

`MUSIC_SOURCES` defaults to `youtube` (ADR 0014 removed the `torrent` value).

- `GET /api/v1/search` merges the local Navidrome library with YouTube Music (songs, albums, artists).
- A hit is a normal track row. Like and playlist writes happen immediately.
- `MUSIC_SOURCES=library` skips YouTube Music. Nothing calls MusicBrainz or a torrent client.

### 2. A small client, not a fork

`services/music-service/internal/ytm` calls YouTube's public innertube JSON (`WEB_REMIX`). The API key and client version are read from the `music.youtube.com` HTML at runtime and cached. Search filters for songs, albums and artists are YouTube's own request parameters.

Playback does not reimplement signature deciphering. `internal/ytdlp` runs the yt-dlp already installed on the machine with `youtube:player_client=android`. The default web client is rejected by YouTube as a bot; the Android client is required. The resolved URL must be `https` on `googlevideo.com`. music-service proxies it with Range and does not write the audio to disk.

### 3. Stable rows

Migration `000004` adds `youtube_video_id` and browse ids. Upserts are idempotent. Likes point at `music.tracks` as before. `stream-url` returns a signed URL immediately (the player keeps its normal loading state while the proxy resolves yt-dlp). Covers are `ytimg-` or an allow-listed thumbnail URL, never an arbitrary host.

## Consequences

- A search for music that is not in the CC0 library can play without Prowlarr indexers.
- YouTube can block the Android client or expire URLs. The resolver caches a URL until shortly before its `expire` parameter and does not log the URL.
- Streaming from YouTube is against YouTube's terms of service. That risk sits with the operator. This service does not ship piracy indexers and does not store the audio for redistribution.
- Setting `MUSIC_SOURCES=library` turns YouTube Music off without a code change.
