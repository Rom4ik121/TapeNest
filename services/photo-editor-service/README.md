# photo-editor-service

Stores photos the user uploaded in «Мои видео» and renders an edit: crop,
rotate, straighten, exposure, color, a filter, text, and JPEG or PNG export.
Rendering is pure Go. Nothing here talks to the internet or to download-service.

Contract: [`docs/api/photo-editor.openapi.yaml`](../../docs/api/photo-editor.openapi.yaml).

| Binary | Role |
|---|---|
| `cmd/server` (:8098) | HTTP API behind api-gateway |
