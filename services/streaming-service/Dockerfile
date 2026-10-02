# syntax=docker/dockerfile:1.7
# streaming-service (stage 4): API :8094 and worker :8095.
# ffmpeg builds the licensed preview (a generated test pattern, not a film) on first start.
FROM golang:1.22.12-alpine3.21@sha256:1699c10032ca2582ec89a24a1312d986a3f094aed3d5c1147b19880afe40e052 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/streaming-service ./cmd/server && \
    go build -ldflags="-s -w" -o /out/streaming-worker ./cmd/worker && \
    go build -ldflags="-s -w" -o /out/streaming-admin ./cmd/admin

FROM debian:12-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
# hadolint ignore=DL3008
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates tzdata ffmpeg && \
    rm -rf /var/lib/apt/lists/* && \
    useradd --system --uid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin tapenest && \
    install -d -o 10001 -g 10001 /var/lib/tapenest/preview /var/lib/tapenest/cache
COPY --from=build /out/streaming-service /out/streaming-worker /out/streaming-admin /usr/local/bin/
ENV CINEMA_PREVIEW_DIR=/var/lib/tapenest/preview CINEMA_CACHE_DIR=/var/lib/tapenest/cache
USER 10001:10001
EXPOSE 8094 8095
ENTRYPOINT []
CMD ["streaming-service"]
