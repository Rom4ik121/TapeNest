# ADR 0014: Video editor and photo editor are separate services

- Status: Accepted
- Date: 2026-10-03

## Context

A user can already paste a video link. The bot and download-service fetch that file
with yt-dlp and store it in MinIO. Editing that file, and editing the user's own
photos, was still Post-MVP (MediaHub). Putting ffmpeg next to the downloader would
mix two jobs: fetching from the internet, and rendering a file the user already has.

Music playback in WavePlayer stays as it is. CineNest stays a cinema catalog and is
not extended here.

## Decision

1. `services/video-editor-service` (API :8096, worker :8097) edits one finished
   download. The only seam with download-service is
   `GET /internal/v1/downloads/{id}/source`, which returns the object key. The editor
   reads bucket `media` and writes bucket `video-edits`. The recipe covers trim,
   split, reorder, speed, crop, rotate, volume, text, a music-bed upload, transitions,
   and export. ffmpeg is invoked by argument vector, never a shell.
2. `services/photo-editor-service` (API :8098, no worker) stores uploads in bucket
   `photos` and renders crop, rotate, straighten, exposure, color, filters, text, and
   JPEG or PNG export in process. Photos are not downloads.
3. api-gateway proxies `/api/v1/video/*` and `/api/v1/photos/*`. An empty URL still
   answers 501.
4. The mini app is `apps/mediahub` at `/mediahub/`. The home screen is «Мои видео»
   with two segments, Videos and Photos. Each editor is a horizontal tab bar sized
   for a phone. The bot gains an optional «Мои видео» button and `/videos` when
   `MINIAPP_MEDIAHUB_URL` is set. The menu button stays WavePlayer.
5. SQL adapters live under `internal/repo/db` so unit coverage does not depend on
   Postgres. Behavior is tested with an in-memory store.

## Consequences

- Export of a video is asynchronous. A photo export returns in the same request.
- Presigned links use `S3_PUBLIC_URL`, so a tunnel change restarts the editors the
  same way it restarts download-service.
- WavePlayer's player code is unchanged. Vite on :5173 forwards `/mediahub`.
