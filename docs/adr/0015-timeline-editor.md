# ADR 0015: A timeline editor over the owner's downloads

- Status: Accepted
- Date: 2026-10-03
- Extends ADR 0014

## Context

Rename, delete and a single trim are not enough once video is "the file I downloaded".
The owner should be able to assemble those files: several clips, a cut, order, speed,
crop, rotation, loudness, timed text, a music bed and a transition, then keep the result
in the same library. CapCut is the feel to aim at. Its code, assets and name are not used.
No new download source, catalog or torrent is added. ffmpeg is already in the download image.

## Decisions

1. **The editor lives at WavePlayer `#/videos/:id/edit`.** Music screens stay as they are.
   Russian copy and the existing light/dark palette are unchanged.
2. **Preview is in the browser** (playback, trim handles, overlays). **Export is ffmpeg**
   on download-service: `POST /api/v1/downloads/compose`. Every `jobId` must be a finished
   download of the same user. The render is a new `edit:` object, same ownership rules as trim.
3. **The timeline is small on purpose:** at most 8 clips and 8 texts, speeds 0.5/1/1.5/2,
   rotate by quarter turns, a fade or a wipe (or a hard cut), one extra audio bed taken from
   another owned download. Output is 1280×720 and at most 10 minutes.
4. **Join length is the same in the mini app and in Go:** a transition overlaps by
   min(requested, 45% of either neighbour), clamped to 0.2–1.5s. A hard cut does not overlap.

## Consequences

- If ffmpeg is missing, rename and delete still work. Export and trim return `EDIT_UNAVAILABLE`.
- A bad timeline is `TIMELINE_INVALID`, not a download error.
- Shared source files are not rewritten. Deleting the export removes its object when nothing else references it.
