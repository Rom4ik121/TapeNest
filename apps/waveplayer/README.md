# WavePlayer — TapeNest music mini app

Rewritten from scratch on 2026-09-25 (stage 0). Same stack and the same API
contract as the original, with the known bugs fixed (see below).

**Stack:** React 18.3 · TypeScript 5.6 (strict, `noUncheckedIndexedAccess`) ·
Vite 5 · TanStack Query 5 · Zustand 4 · Tailwind 3 · @telegram-apps/sdk-react
2.0.25 (→ @telegram-apps/sdk 2.11.3) · react-i18next (ru/en) · MSW 2 · Vitest + RTL.

## Run

```bash
cd apps/waveplayer
cp .env.example .env     # VITE_USE_MOCKS empty = no mocks: real api-gateway + music-service (stage 3)
npm ci
npm run dev              # http://localhost:5173 — /api is proxied to api-gateway (:8080)
npm run lint && npm run typecheck && npm test && npm run build
npm run format           # prettier (single quotes, 120 cols)
```

Mocks per API domain (`VITE_USE_MOCKS`, ADR 0004 update):

| Value | auth (`/auth/*`, `/me`) | music (tracks/playlists/wave/events/position) |
|---|---|---|
| empty / `none` / `false` (dev and prod default since stage 3) | real api-gateway | real music-service |
| `music` | real api-gateway | MSW (frontend work without music-service/Navidrome) |
| `all` / `true` | MSW (guest login in a plain browser) | MSW |

Unit tests use the MSW handlers directly, whatever this flag says.

Media URLs (stage 3): `Track.coverUrl` and `stream-url` return **signed links relative to the
API origin** (`/api/v1/stream/...`). They are used as-is on the same origin (Vite proxy / nginx)
and resolved against `VITE_API_URL` when it is set (`src/shared/api/media.ts`). A 503
`service: "streaming"` from `stream-url` (Navidrome down) shows "stream unavailable" in the
player and skips to the next track; the catalog keeps working. Through ngrok-free, audio and
covers load after the one-time "Visit site" click (ADR 0009).

Real auth needs signed initData. Use either:
- Telegram itself: `make miniapp-up` from the repo root starts gateway, bot-service, Vite and
  ngrok (see `tools/dev/`);
- or a browser with a signed launch fragment:
  `http://localhost:5173/$(cd ../../tools/initdata-mock && go run . -format hash -theme light -lang en)`.

A plain browser without initData shows the "Open in Telegram" screen.

Telegram integration (`src/shared/telegram`):
- viewport expand;
- `--tg-viewport-*` CSS vars, which drive stable height and safe/content-safe area insets;
- BackButton handler stack (page → sheet → full player);
- MainButton for the create-folder sheet, with an in-page button fallback outside Telegram;
- haptics for navigation, play/pause/skip, like and success/error;
- header/background colours synced to the theme.

## API contract

Source of truth: [`docs/api/waveplayer-contract.md`](../../docs/api/waveplayer-contract.md)
and [`waveplayer.openapi.yaml`](../../docs/api/waveplayer.openapi.yaml). Paths must not change.

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/v1/auth/telegram` | initData → JWT pair |
| POST | `/api/v1/auth/refresh` | refresh JWT (rotation) |
| POST | `/api/v1/auth/logout` | revoke session (gateway) |
| GET | `/api/v1/me` | session check on start (401 → refresh → retry) |
| GET | `/api/v1/tracks/recent` · `popular` · `liked` | `Page<Track>`, `?cursor=&limit=` |
| GET | `/api/v1/tracks/search?q=` | search (300 ms client debounce), paginated |
| GET | `/api/v1/tracks/:id/stream-url` | presigned URL (short TTL) |
| POST/DELETE | `/api/v1/tracks/:id/like` | like / unlike |
| GET/PUT | `/api/v1/tracks/:id/position` | playback position (**new**, ADR 0002) |
| GET/POST/PATCH/DELETE | `/api/v1/playlists…` | folders and their tracks |
| POST | `/api/v1/wave/sessions` | "My Wave" session (first batch) |
| GET | `/api/v1/wave/sessions/:id/tracks?after=` | more wave tracks |
| POST | `/api/v1/wave/sessions/:id/feedback` | like / skip |
| POST | `/api/v1/events/track-listened` | 50 % and completion events |

## Structure (feature-sliced)

```
src/
├── app/             App shell, routes, tab bar
├── entities/
│   ├── auth/        token store (persist) + session logic (initData → JWT)
│   ├── player/      audioEngine (single HTMLAudioElement), playerStore (factory, testable),
│   │                listenTracker, positionSaver, session (localStorage)
│   └── track/       queries (infinite, cursor), TrackRow/TrackList, add-to-folder sheet
├── features/        home, wave, search, library (+liked, playlist), player (bar + full screen)
├── i18n/            ru.json, en.json (language from initData.user.language_code, fallback ru)
├── mocks/           MSW handlers + fetch fallback + procedural audio (dev only)
└── shared/
    ├── api/         typed client (timeout w/o AbortSignal.timeout, shared refresh), endpoints
    ├── telegram/    the ONLY module importing the Telegram SDK (ESLint-enforced)
    ├── theme/       palette tokens (CSS variables), light/dark sync
    ├── lib/, ui/    helpers and primitives
```

## Design

Palette: cream `#E7E4DE`, amber `#EEAA11`, teal `#4FB3B3`, magenta `#BB3381`,
deep purple `#3F1D50`, near-black `#16141C`; gradients 01–05 as Tailwind
`bg-grad-01…05`. Light theme = cream backgrounds, magenta accent; dark theme =
`#16141C`/purple surfaces, amber accent. Theme follows Telegram `colorScheme`
(theme params), fallback `prefers-color-scheme`. WCAG AA contrast of token
pairs is asserted in `src/shared/theme/theme.test.ts`.

## Fixed bugs of the old app

| Old bug | Fix |
|---|---|
| mocks `data.ts` TDZ (TRACKS read `mockDb` before declaration) | `mockDb` declared first; `liked` computed per response (test) |
| `isPlaying` true even when autoplay was rejected | status driven by media events; rejected `play()` → `blocked` ("tap to continue") (test) |
| stale stream-url race on fast switching | per-load token + AbortController; late responses ignored (test) |
| listened-events Set never cleared | tracker reset on every play; replays count again (test) |
| only first page loaded | `useInfiniteQuery` + IntersectionObserver, follows `nextCursor` (test) |
| stored JWT not checked against Telegram user | tokens bound to `tg:<id>` identity + `user.telegramId` (test) |
| `AbortSignal.timeout` unsupported in old iOS WebViews | own `createTimedSignal` (test) |
| no playback position saving | 5 s debounce + forced on pause/switch/hidden/pagehide, server + local, restore (tests) |
| no i18n | ru/en dictionaries, plurals, key-parity test |
| no tests | Vitest: 87 tests (store, api, auth/session flow, mocks, config, theme contrast, i18n, RTL) |
| `User.id` numeric | UUID string + `telegramId` |

## Known limitations

- Background playback in Telegram (iOS) — platform limitation, see ADR 0001.
- Micro-gap between tracks (HTMLAudioElement; Web Audio is Post-MVP).
- Lists aren't virtualised yet; add `@tanstack/react-virtual` when real catalogs grow.
