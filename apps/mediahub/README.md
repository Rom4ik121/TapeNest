# MediaHub — «Мои видео»

Mini app for a file the user already downloaded, and for their own photos.
WavePlayer stays the music player. CineNest stays the cinema catalog.

Served at `/mediahub/` (Vite :5175). The home screen has two segments:

- **Видео** — paste a link (download-service), then open a tabbed editor: clip, timing, look, audio, text, export.
- **Фото** — upload a JPEG or PNG and edit it in tabs: crop, look, color, text, export.

Editors talk to api-gateway, which proxies `video-editor-service` and `photo-editor-service` (ADR 0014).
