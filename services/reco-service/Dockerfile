# syntax=docker/dockerfile:1.7
# reco-service image (My Wave recommender, ADR 0010): one image, two entrypoints.
#   docker build -t tapenest/reco-service services/reco-service
#   API:    docker run tapenest/reco-service                           (/reco-service)
#   Worker: docker run --entrypoint /reco-worker tapenest/reco-service
# The worker decodes audio with ffmpeg for the pure-Go DSP analyser, so the
# runtime is Debian slim (distroless has no ffmpeg). Both binaries are CGO-free.
FROM golang:1.22.12-alpine3.21@sha256:1699c10032ca2582ec89a24a1312d986a3f094aed3d5c1147b19880afe40e052 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/reco-service ./cmd/server && \
    go build -ldflags="-s -w" -o /out/reco-worker ./cmd/worker && \
    go build -ldflags="-s -w" -o /out/reco-eval ./cmd/reco-eval

FROM debian:12-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
# hadolint ignore=DL3008
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates tzdata ffmpeg && \
    rm -rf /var/lib/apt/lists/* && \
    useradd --system --uid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin tapenest
COPY --from=build /out/reco-service /out/reco-worker /out/reco-eval /
USER 10001:10001
ENV FFMPEG_BIN=/usr/bin/ffmpeg
EXPOSE 8086 8087
ENTRYPOINT ["/reco-service"]
