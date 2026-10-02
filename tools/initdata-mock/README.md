# tools/initdata-mock

Generates **valid signed Telegram Mini App `initData`** (HMAC-SHA256 per the
Telegram algorithm) for local development and tests (spec §11). Go 1.22,
stdlib only. The package `initdata` also has `Validate` (signature + 24 h TTL
+ future-skew check) with table-driven tests — the same algorithm the
api-gateway `internal/auth` must implement in stage 1.

The bot token is read from an env var (default `TELEGRAM_BOT_TOKEN`) and is
never printed.

```bash
cd tools/initdata-mock
go test ./...
TELEGRAM_BOT_TOKEN=... go run . -user-id 42 -first-name Roma -lang ru      # raw initData
TELEGRAM_BOT_TOKEN=... go run . -format json                              # POST /api/v1/auth/telegram body
TELEGRAM_BOT_TOKEN=... go run . -format hash -theme light                 # URL fragment:
#   open "http://localhost:5173/$(go run . -format hash)" → the app behaves as inside Telegram
TELEGRAM_BOT_TOKEN=... go run . -validate "$(go run . -age 90000)"         # → INVALID: expired
```

Flags: `-user-id -first-name -last-name -username -lang -age -query-id
-start-param -format raw|json|hash -theme dark|light -token-env -validate`.

Note: Telegram also adds an Ed25519 `signature` field for third-party
validation; it cannot be forged locally. A placeholder is included by default
(`-signature`, part of the HMAC input) because `@telegram-apps/sdk` 2.11 refuses
to parse initData without it. It is not needed for bot-token HMAC
validation.
