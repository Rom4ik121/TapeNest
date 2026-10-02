# ADR 0004 — Dev mocks and dev tunnel

- **Status:** Accepted (2026-09-25); amended in stage 1 — see the update below and ADR 0005
- **Context:** spec §11 (local debugging), §19.6 (mocks only behind a dev flag)

**Mocks.** No backend exists yet, so `VITE_USE_MOCKS` defaults to `true`
(set `false` in stage 1). MSW handlers implement the full contract. The MSW
Service Worker is used when available; otherwise (iOS Telegram's WKWebView has
no Service Workers, private mode, tunnel interstitial breaking the SW script)
the same handlers are served by an in-page `fetch` wrapper using MSW's
`getResponse`. Mock streaming uses generated WAV blobs. Mocks are loaded through
a dynamic import only when the flag is on.

Mock auth accepts real Telegram initData without verifying the signature (the
client cannot hold the bot token); signature/TTL validation belongs to
api-gateway `internal/auth` (stage 1) — the reference algorithm and tests live
in `tools/initdata-mock/initdata`.

**Tunnel.** `tools/dev/miniapp-up.sh` runs the Vite dev server, an ngrok HTTPS
tunnel and a dependency-free dev bot responder (`tools/dev/bot-dev`, long
polling: `/start` → `web_app` button; keeps the chat menu button on the current
tunnel URL). All DEV-ONLY; the real bot-service is Go + webhook (stage 1).
Known ngrok free-plan caveat: a one-time "Visit site" interstitial per browser.

## Update (stage 1)

- `VITE_USE_MOCKS` is now **per domain**: `music` (the dev default) sends auth to the real
  api-gateway and keeps tracks, playlists, wave, events and position mocked until
  music-service ships (stage 3).
  - `all` / `true` mocks everything (offline).
  - `none` / `false` disables mocks. Production builds default to this and do not bundle MSW.
- With real auth, the music mocks accept any Bearer token, because they cannot verify gateway
  JWTs.
- `tools/dev/bot-dev` was removed. The real bot-service runs on a webhook (see ADR 0005).
