# bot-service

Telegram bot @tapenest_bot, running on a **webhook** (no long polling).

Stack: Go 1.22, chi, cleanenv, go-redis (update dedupe, download events) and gobreaker (gateway
client).

## Behaviour

- `POST ${TELEGRAM_WEBHOOK_PATH}` (default `/tg/webhook`):
  - requires `X-Telegram-Bot-Api-Secret-Token == TELEGRAM_WEBHOOK_SECRET` (constant-time
    compare), otherwise `401`;
  - dedupes `update_id` in Redis for 24h;
  - always answers `200` quickly to a valid call, so Telegram does not retry because of handler
    errors.
- Private chats only:
  - `/start` sends a greeting and an inline `web_app` button to the WavePlayer mini app;
  - `/help` sends a help text;
  - an unknown command gets a hint.
- Links to supported public video hosts (YouTube, VK, RuTube, TikTok, Vimeo and others yt-dlp already extracts) are detected from message entities
  (UTF-16 offsets) with a regex fallback. They are forwarded to the gateway via
  `POST /internal/v1/bot/downloads`.
  - Download flow (stage 2, ADR 0008):
    1. The bot answers right away with an ack ("⏳ Ссылка принята (YouTube), проверяю…").
    2. It calls the gateway with `statusMessageId` (the ack) and `replyToMessageId` (the user's
       message).
    3. Errors from the gateway are shown by editing the ack. Codes: `UNSUPPORTED_SOURCE`,
       `PLAYLIST_NOT_SUPPORTED`, `QUOTA_ACTIVE`/`QUOTA_DAILY` (429), 501 if download-service is
       not deployed, and 503.
  - `internal/downloads` consumes the Redis stream `download:events` (consumer group
    `bot-service`):
    - progress edits the ack: probing → a 10-cell bar in 25 % steps → storing → retry notice;
    - `video.downloaded` with a file ≤ `TELEGRAM_UPLOAD_LIMIT` (50 MB): the bot streams the object
      from MinIO (internal presigned URL) into `sendVideo`, replying to the user's message, then
      edits the ack to "✅ Готово";
    - a larger file, or an upload that Telegram rejects: the ack shows the size and a
      "Download" URL button with the 1 h public link;
    - `download.failed`: a localized reason (`dl.error.<kind>`);
    - delivery is at least once: pending entries are re-read, XAUTOCLAIM runs after 2 min, and
      `SETNX bot:dl:done:<job>` prevents a double upload. A blocked bot or missing chat drops the
      event.
- Texts are RU/EN (`internal/i18n/{ru,en}.json`) and follow the user's `language_code`:
  `en*` gets EN, everything else gets RU.
- When `BOT_SETUP_ON_START=true`, startup calls:
  - `setWebhook(url, secret_token, allowed_updates=[message])`;
  - `setChatMenuButton(web_app)`;
  - `setMyCommands` (default, ru, en).

## Run

```bash
set -a; . ../../.env; set +a
TELEGRAM_WEBHOOK_URL=https://<public-host>/tg/webhook MINIAPP_WAVEPLAYER_URL=https://<public-host>/ \
  go run ./cmd/server
```

In dev, `make miniapp-up` does this against the ngrok URL (see ADR 0005). `tools/dev/url-watch.sh`
re-points the webhook and menu button when the URL changes.

Endpoints: `GET /healthz`, `GET /readyz` (Redis), `POST /tg/webhook`.

Commands: `/start`, `/help`, `/videos` (the download library). `/cinema` answers that there is no film catalog and points at a pasted link.

## Environment

`TELEGRAM_BOT_TOKEN`, `TELEGRAM_WEBHOOK_URL`, `TELEGRAM_WEBHOOK_SECRET`, `TELEGRAM_WEBHOOK_PATH`,
`TELEGRAM_API_BASE`, `MINIAPP_WAVEPLAYER_URL`, `MINIAPP_VIDEOS_URL`, `BOT_SERVICE_PORT`, `BOT_SETUP_ON_START`,
`BOT_MENU_BUTTON_TEXT`, `REDIS_URL`, `GATEWAY_URL`, `GATEWAY_TIMEOUT`, `INTERNAL_API_TOKEN`,
`LOG_LEVEL`, `APP_ENV`, `DOWNLOAD_EVENTS_ENABLED` (default `true`), `TELEGRAM_UPLOAD_LIMIT`
(default `50000000`; raise it only with a local Bot API server).

## Tests

`make go-test` runs unit tests with a fake Telegram API, a fake gateway and miniredis. Coverage
gate: ≥70%.
