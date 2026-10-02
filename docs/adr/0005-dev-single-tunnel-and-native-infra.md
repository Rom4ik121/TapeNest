# ADR 0005 — One dev tunnel for mini app + API + webhook; native infra without Docker

- **Status:** Accepted (2026-09-25)
- **Context:** spec §11 (local debugging), stage 1 (bot-service webhook, api-gateway auth).
  The dev box has no Docker; ngrok free gives one random HTTPS URL per agent session.

## Decision

**Single public URL.** Only one ngrok endpoint is exposed (the Vite dev server,
`:5173`). The Vite dev server (and `vite preview`) proxies:

| Path            | Target (env)                                         |
|-----------------|------------------------------------------------------|
| `/api/*`        | api-gateway, `GATEWAY_URL` (default `127.0.0.1:8080`) |
| `/tg/*`         | bot-service, `BOT_SERVICE_URL` (default `127.0.0.1:8081`) |
| everything else | WavePlayer (Vite)                                    |

- The mini app calls the API with **same-origin** relative URLs (`VITE_API_URL` empty),
  so the ngrok URL never has to be baked into the frontend, and CORS is only a fallback.
- The webhook is `setWebhook(<ngrok>/tg/webhook, secret_token)`. bot-service checks
  `X-Telegram-Bot-Api-Secret-Token` in constant time.
- Proxies set `X-Forwarded-*` (`xfwd`); the gateway trusts them only with `TRUST_PROXY_HEADERS=true`.
  That is needed for per-IP auth rate limits.
- **Production** uses the same path layout via nginx (`deploy/nginx/dev.conf`: `/api` → gateway,
  `/tg/` → bot-service), so nothing is Vite-specific.

Rejected alternatives:
- A multi-endpoint ngrok config: it needs more agent sessions/URLs, and the free plan gives only
  one random URL.
- A separate Go reverse proxy: it would be one more process doing what Vite already does.

**URL auto-sync.**
- `tools/dev/url-watch.sh` polls the ngrok API every 30 s. If the URL changed, it restarts
  bot-service, which on start calls `setWebhook`, `setChatMenuButton` (web_app → new URL) and
  `setMyCommands`.
- A running ngrok is reused across `make miniapp-up`, so the URL stays stable.

**Long polling removed.** Telegram rejects `getUpdates` while a webhook is set, so the
stage-0 long-polling dev bot (`tools/dev/bot-dev`) was deleted.

**Native infra (no Docker).**
- `tools/dev/infra-native.sh` runs PostgreSQL 16 (PGDG apt, cluster `16/main`) and Redis 7.4
  (built from source into `/opt/redis-7.4`).
- It creates the `tapenest` / `tapenest_test` roles and DBs from `.env` and applies
  `deploy/pg/init/*.sql` (schema per service, owned by the app role).
- `deploy/docker-compose.dev.yml` remains the reference for machines with Docker.
  Profile `app` adds the api-gateway and bot-service containers.

## Consequences

- The dev stack is `make miniapp-up` / `make miniapp-down`. Pid files and logs are in
  `/workspace/logs`.
- ngrok-free shows a one-time HTML interstitial to browsers without the
  `ngrok-skip-browser-warning` header. The app sends this header on API calls, and MSW skips
  its Service Worker on `*.ngrok-free.app` (the SW script request can't carry the header) and
  uses the in-page fetch fallback instead.
