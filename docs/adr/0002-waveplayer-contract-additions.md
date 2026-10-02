# ADR 0002 — WavePlayer contract additions

- **Status:** Accepted (2026-09-25)
- **Context:** spec §5.2 (UUID users), §5.4 (`playback_positions`), §6, §7.2

The WavePlayer frontend was rewritten from scratch (stage 0). Its original
contract is preserved verbatim in `docs/api/waveplayer-contract.md`; paths did
not change. Three backwards-compatible additions were required by the spec:

1. **Playback positions:** `GET /api/v1/tracks/:id/position` →
   `{trackId, positionSec, updatedAt}` (404 if none) and
   `PUT /api/v1/tracks/:id/position` `{positionSec}` → 204. The old client had
   no position saving at all.
2. **Cursor params are sent:** list endpoints receive `?cursor=<nextCursor>&limit=`
   (default 20, max 100). The old client ignored `nextCursor` and only showed page 1.
3. **`User.id` is a UUID string** (was numeric) plus `telegramId: number` and
   `languageCode` — matches `users.id UUID v4`, `telegram_id BIGINT UNIQUE`.

Machine-readable: `docs/api/waveplayer.openapi.yaml` (OpenAPI 3.1, spec §3.1 #3).
