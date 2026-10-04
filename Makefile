# TapeNest monorepo — `make help` lists targets.
SHELL := /bin/bash
COMPOSE ?= $(shell docker compose version >/dev/null 2>&1 && echo "docker compose" || echo "docker-compose")
COMPOSE_FILE := deploy/docker-compose.dev.yml
DC := $(COMPOSE) --env-file $(if $(wildcard .env),.env,.env.example) -f $(COMPOSE_FILE)
FE := apps/waveplayer
# every mini app gets lint/typecheck/test/build (FE_APPS=apps/cinenest make fe-test for one)
FE_APPS ?= apps/waveplayer apps/cinenest apps/mediahub
FE_EACH = @for a in $(FE_APPS); do echo "==> $$a"; (cd $$a && $(1)) || exit 1; done
GO_MODULES := $(shell find services tools -name go.mod -not -path '*/node_modules/*' -exec dirname {} \; 2>/dev/null)
GO_SERVICES := $(shell find services -name go.mod -exec dirname {} \; 2>/dev/null)
GOLANGCI ?= $(shell command -v golangci-lint 2>/dev/null || echo $(HOME)/.local/bin/golangci-lint)
# TEST_DATABASE_URL / TEST_REDIS_URL for Go integration tests come from .env when present
OPENAPI_VALIDATOR ?= $(or $(wildcard /workspace/.venv-tools/bin/openapi-spec-validator),openapi-spec-validator)
ENV_EXPORT := $(if $(wildcard .env),set -a; . ./.env; set +a;,)

.DEFAULT_GOAL := help
.PHONY: help dev infra-up infra-up-all infra-down infra-logs infra-ps compose-config app-up \
        infra-native infra-native-down music-seed e2e-music e2e-reco e2e-acquisition e2e-ytm arr-up arr-down reco-eval fe-install fe-dev cn-dev mh-dev fe-lint fe-typecheck fe-test fe-build \
        go-lint go-test go-build sqlc migrate openapi hadolint lint test build proto initdata \
        miniapp-up miniapp-down stack-status clean

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n",$$1,$$2}'

dev: infra-up fe-dev ## Infra in Docker + WavePlayer dev server (mocks on)

# ── infrastructure ─────────────────────────────────────────────────────────
infra-up: ## Start core infra (postgres, redis, minio, navidrome, torrserver)
	@mkdir -p data/music
	$(DC) up -d
infra-up-all: ## Core infra + observability (prometheus, grafana) + edge (nginx)
	@mkdir -p data/music
	$(DC) --profile observability --profile edge up -d
infra-down: ## Stop infra (volumes kept)
	$(DC) --profile observability --profile edge down
infra-logs: ## Follow infra logs
	$(DC) logs -f --tail=100
infra-ps: ## Infra status
	$(DC) ps
compose-config: ## Validate docker-compose.dev.yml (all profiles)
	$(DC) --profile app --profile observability --profile edge config -q && echo "compose OK"
app-up: ## Infra + Go services (api-gateway, bot-service) in Docker
	@mkdir -p data/music
	$(DC) --profile app up -d --build
infra-native: ## No Docker: native PostgreSQL 16 + Redis 7.4 + MinIO + Navidrome (tools/dev/infra-native.sh)
	tools/dev/infra-native.sh up
infra-native-down: ## Stop native PostgreSQL/Redis/MinIO/Navidrome
	tools/dev/infra-native.sh down
music-seed: ## Demo music: CC0/public-domain recordings (Wikimedia Commons, Musopen) → MUSIC_DIR for Navidrome
	@$(ENV_EXPORT) python3 tools/dev/seed-music.py

# ── frontend ───────────────────────────────────────────────────────────────
fe-install: ## npm ci for all mini apps
	$(call FE_EACH,npm ci)
fe-dev: ## WavePlayer dev server on :5173
	cd $(FE) && npm run dev
cn-dev: ## CineNest dev server on :5174 (base /cinenest/, cinema mocks on)
	cd apps/cinenest && npm run dev
mh-dev: ## MediaHub («Мои видео») dev server on :5175 (base /mediahub/)
	cd apps/mediahub && npm run dev
fe-lint: ## ESLint (0 warnings), all mini apps
	$(call FE_EACH,npm run lint)
fe-typecheck: ## tsc --noEmit (strict), all mini apps
	$(call FE_EACH,npm run typecheck)
fe-test: ## Vitest, all mini apps
	$(call FE_EACH,npm test)
fe-build: ## Production builds, all mini apps
	$(call FE_EACH,npm run build)

# ── Go ──────────────────────────────────────────────────────────────────────
go-lint: ## golangci-lint (.golangci.yml: errcheck, govet, staticcheck, revive, gosec, gofumpt…)
	@for m in $(GO_MODULES); do echo "==> $$m"; (cd $$m && $(GOLANGCI) run --config $(CURDIR)/.golangci.yml ./...) || exit 1; done
go-test: ## go test -race; services need coverage ≥ 70 % (integration: TEST_DATABASE_URL/TEST_REDIS_URL)
	@$(ENV_EXPORT) for m in $(GO_MODULES); do echo "==> $$m"; min=0; case $$m in services/*) min=70;; esac; \
	(cd $$m && $(CURDIR)/tools/ci/go-coverage.sh $$min) || exit 1; done
go-build: ## Build Go service binaries into bin/
	@mkdir -p bin; for m in $(GO_SERVICES); do echo "==> $$m"; (cd $$m && CGO_ENABLED=0 go build -o $(CURDIR)/bin/$$(basename $$m) ./cmd/server && \
	  if [ -d cmd/worker ]; then CGO_ENABLED=0 go build -o $(CURDIR)/bin/$$(basename $$m | sed 's/-service$$//')-worker ./cmd/worker; fi) || exit 1; done
sqlc: ## Regenerate sqlc code (services with sqlc.yaml)
	@for m in $(GO_SERVICES); do [ -f $$m/sqlc.yaml ] && (cd $$m && sqlc generate && echo "sqlc: $$m"); done; true
migrate: ## Apply api-gateway migrations (golang-migrate, schema gateway) using .env
	@$(ENV_EXPORT) cd services/api-gateway && go run ./cmd/server -migrate
hadolint: ## Lint Dockerfiles
	hadolint $$(find services -name Dockerfile)

lint: fe-lint fe-typecheck go-lint ## All linters
test: fe-test go-test ## All tests
build: fe-build go-build ## All builds

openapi: ## Validate docs/api/*.openapi.yaml (openapi-spec-validator; OPENAPI_VALIDATOR=path)
	@for f in docs/api/*.openapi.yaml; do $(OPENAPI_VALIDATOR) $$f || exit 1; done

proto: ## Placeholder: MVP contract is REST + OpenAPI 3.1 (spec §3.1 #3); gRPC is Post-MVP
	@echo "No proto generation in MVP. Contract: docs/api/waveplayer.openapi.yaml"

initdata: ## Signed test initData (needs TELEGRAM_BOT_TOKEN in env), ARGS="-format hash"
	cd tools/initdata-mock && go run . $(ARGS)

# ── dev tunnel (Telegram) ─────────────────────────────────────────────────
miniapp-up: ## DEV: native infra, music-service + worker, gateway, bot-service (webhook+menu), Vite ×2 (WavePlayer, CineNest), ngrok, url-watch
	tools/dev/miniapp-up.sh
miniapp-down: ## DEV: stop the dev stack (KEEP_NGROK=1 keeps the tunnel URL)
	tools/dev/miniapp-down.sh
e2e-auth: ## DEV: real auth e2e through the public URL (login, /me, refresh rotation, reuse, logout)
	@bash tools/dev/e2e-auth.sh
e2e-music: ## DEV: music API e2e through the public URL (catalog, signed stream + Range, likes, playlists, wave, position, events)
	@bash tools/dev/e2e-music.sh

e2e-reco: ## DEV: My Wave recommender e2e (reco strategy + reasons, modes, event ingest, breaker fallback when reco stops)
	@bash tools/dev/e2e-reco.sh

e2e-acquisition: ## DEV: invisible acquisition e2e (unified search, play-before-download via the CC0 test indexer, likes, import, quotas)
	@bash tools/dev/e2e-acquisition.sh

e2e-ytm: ## DEV: YouTube Music search + stream (no file saved)
	@bash tools/dev/e2e-ytm.sh

arr-up: ## DEV: Lidarr + Prowlarr + qBittorrent-nox natively (pinned versions, checksum-verified, 127.0.0.1 only)
	tools/dev/arr-native.sh up
arr-down: ## DEV: stop Lidarr + Prowlarr + qBittorrent-nox
	tools/dev/arr-native.sh down

reco-eval: ## Offline evaluation of My Wave (synthetic users; reco vs ADR 0009 heuristic) → docs/reco/evaluation.md
	@cd services/reco-service && GOTOOLCHAIN=$${GOTOOLCHAIN:-local} go run ./cmd/reco-eval -seeds 5 -out ../../docs/reco/evaluation.md
stack-status: ## DEV: status of dev stack processes
	tools/dev/svc.sh status

clean: ## Remove build artefacts
	rm -rf $(FE)/dist $(FE)/coverage bin
