# ADR 0007: CineNest is served under `/cinenest/` on the single origin; bot entry points

- Status: accepted
- Date: 2026-09-25
- Context: stage 1, CineNest skeleton (AI_DEVELOPMENT_INSTRUCTION.md), ADR 0005 (single dev tunnel)

## Context

CineNest is the second mini app. Dev exposes exactly one public HTTPS URL (the ngrok free plan, ADR 0005).
That URL already serves WavePlayer, `/api` (gateway) and `/tg` (webhook). Telegram needs an HTTPS URL for
every web_app button. The bot has one chat menu button.

## Decision

1. **Path prefix.** CineNest is built with Vite `base: '/cinenest/'` and uses a HashRouter, so deep links
   look like `/cinenest/#/title/…`.
   - Dev: WavePlayer's Vite (the tunnel target) proxies `/cinenest` (HTTP and HMR websockets) to CineNest's
     own Vite on :5174. `tools/dev/lib.sh: start_cinenest`, and `miniapp-up` / `svc.sh restart cinenest`.
   - Prod/edge: nginx serves the CineNest `dist/` at `/cinenest/` (dev.conf proxies to the host Vite).
   - Same origin means the same gateway and no CORS. Telegram `initData` is per launch, so auth works the
     same way. Each app keeps its own token storage key, because localStorage is per origin but keys differ.
2. **Bot entry.**
   - `/start` and `/help` messages carry two web_app buttons: WavePlayer and CineNest.
   - A new `/cinema` command (alias `/cinenest`) replies with a CineNest button and is registered via
     setMyCommands (default, ru, en).
   - The **menu button stays WavePlayer**: Telegram allows one per bot, and music is the primary scenario.
   - Everything CineNest-related is optional: without `MINIAPP_CINENEST_URL` the button and the command
     disappear, and `/cinema` answers "not connected".
3. **No shared frontend package yet.** The api client, auth, telegram wrapper and theme are copied from
   WavePlayer (about 1.5k lines). A workspace package would need npm workspaces or a build step for both
   apps and CI changes, while the code is still changing quickly. Revisit this when a third app appears or
   when the copies diverge. Until then, a fix in one copy must be applied to the other; both have the same
   tests.
4. **Mocks per domain.** The cinema API is mocked with MSW until stage 4 (`VITE_USE_MOCKS=cinema`, the dev
   default), while auth goes to the real gateway.

## Consequences

- There is one URL to register, and the webhook, menu button and both apps follow ngrok URL changes
  (url-watch restarts bot-service, which re-registers everything).
- The CineNest bundle is split: hls.js (~130 kB gzip) is loaded only when the player opens.
- Two copies of the shared frontend code must be kept in sync until a shared package is introduced.
