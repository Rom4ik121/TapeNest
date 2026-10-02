# ADR 0014: Video is a pasted link; cinema is not a user-facing surface

- Status: Accepted
- Date: 2026-10-02
- Supersedes the bot and public routing parts of ADR 0007 and the user-facing half of ADR 0013

## Context

TapeNest had three stories in front of the user: WavePlayer (YouTube Music search, play, likes),
CineNest (a film catalog with torrent playback), and “paste a link, download the video”.
The product owner dropped films. Music stays as it is. Video is only a link the download
service already accepts (YouTube, VK, RuTube) — not a search, not a magnet, not an indexer.

## Decisions

1. **The bot does not offer cinema.** `/cinema` (and `/cinenest`, `/film`) replies that there is
   no film catalog and asks for a video link. `/start` and `/help` show WavePlayer and
   «Мои видео». The menu button stays WavePlayer. `MINIAPP_CINENEST_URL` is no longer read.
2. **«Мои видео» is WavePlayer's `#/videos` route**, same origin, same session. Music screens
   and the music nav are unchanged. The public Vite proxy and nginx no longer serve `/cinenest/`.
3. **A pasted link is a library row** for that user (`GET /api/v1/downloads`). The bot still
   acks, edits progress, and sends the file or a one-hour link.
4. **Edits are per user, with ffmpeg already in the download image.** Rename writes
   `display_title` on the job (the shared media title stays). Delete hides the job. Trim
   (`POST /api/v1/downloads/{id}/trim`) cuts `[startSec, endSec)` into a new object under
   `edits/…` and a new done job. Deleting a trim removes that object when nothing else
   references it. Deleting a shared source download does not delete the object, so the next
   paste of the same URL can still hit dedup.
5. **A frame** is extracted with ffmpeg when the worker has a local file (merge path) and again
   when a trim is saved (`posters/<id>.jpg`). Otherwise the mini app uses the source thumbnail.
6. **CineNest and streaming-service stay in the tree** so the build does not have to be
   unwound. They are not linked from the bot or the public dev tunnel.

## Consequences

- Users who type `/cinema` get a short explanation, not a catalog.
- Music (search, play, likes, wave) is untouched.
- Trim needs ffmpeg on the API process. If the binary is missing, rename and delete still work
  and trim returns `EDIT_UNAVAILABLE`.
