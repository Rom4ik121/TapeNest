# ADR 0006 — Refresh sessions in Redis, 501 vs 503, integration tests without testcontainers

- **Status:** Accepted (2026-09-25)
- **Context:** stage 1 api-gateway. The spec fixes JWT access + refresh with rotation and
  revocation "in Redis or DB". It does not fix the storage details, the error semantics for
  services that are not deployed yet, or the integration-test harness.

## Decisions

**Refresh tokens.**
- Refresh tokens are opaque 256-bit random values. Redis stores only `sha256(token)` with the
  TTL `JWT_REFRESH_TTL`, grouped by session id (`sid`, the "family").
- `/auth/refresh` consumes the token atomically (Lua) and issues a new pair (**rotation**).
- Presenting an already-rotated token is **reuse**. The gateway answers `401 REFRESH_REUSED`,
  revokes the whole family and sets a `sid`-revoked marker. That marker makes every access JWT
  of that session fail immediately, without waiting for its TTL.
- `/auth/logout` revokes the family the same way.
- Access tokens are HS256 JWTs carrying `iss`, `aud`, `sub` (the user UUID), `tid` (telegram
  id), `sid` and `role`, with the TTL `JWT_ACCESS_TTL` (15m).

Rationale: Redis is already required for rate limiting and dedupe, and revocation checks stay
O(1). Postgres keeps only durable user data (`gateway.users`).

**Error semantics for upstream services.**

| Situation | Response |
|---|---|
| Upstream URL not configured (service not deployed yet, e.g. music until stage 3, download until stage 2) | `501 NOT_IMPLEMENTED` with `service` and a message naming the stage |
| Upstream configured but failing (timeout, 5xx, breaker open) | `503 SERVICE_UNAVAILABLE` |

These are honest responses, never fake successes. The frontend treats both as
"section unavailable" (partial degradation) and words them differently: "coming soon" for 501,
"temporarily unavailable" for 503. The bot answers links with the documented 501 text.

**Integration tests.**
- Tests needing Postgres or Redis read `TEST_DATABASE_URL` / `TEST_REDIS_URL` and are skipped
  when these are unset.
- CI provides digest-pinned `postgres:16` and `redis:7.4` service containers. Locally, the
  native infra from ADR 0005 is used.
- testcontainers-go was not adopted: it requires a Docker daemon, which the dev box lacks.
- Unit tests use miniredis and a fake Telegram API (`httptest`).

## Consequences

- Coverage gates: business logic ≥ 70% (`tools/ci/go-coverage.sh`, generated sqlc code excluded).
- Losing Redis logs everyone out. That is acceptable: users re-login silently with initData.
  The frontend's 401 → refresh → initData re-login path handles it.
