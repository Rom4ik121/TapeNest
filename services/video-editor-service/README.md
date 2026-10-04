# video-editor-service

Edits a file the user already downloaded. Download-service still only fetches
the video (yt-dlp). This service reads that object and renders a new file with
ffmpeg: trim, split, reorder, speed, crop, rotate, volume, text, a music bed
and transitions between clips.

Contract: [`docs/api/video-editor.openapi.yaml`](../../docs/api/video-editor.openapi.yaml).

| Binary | Role |
|---|---|
| `cmd/server` (:8096) | HTTP API behind api-gateway |
| `cmd/worker` (health :8097) | Renders queued exports |

`GET /internal/v1/downloads/{id}/source` on download-service is the only seam:
the editor never talks to YouTube, VK or RuTube itself.
