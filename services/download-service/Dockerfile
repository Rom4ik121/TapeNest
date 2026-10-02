# syntax=docker/dockerfile:1.7
# download-service image (spec stage 2): one image, two entrypoints.
#   docker build -t tapenest/download-service services/download-service
#   API:    docker run tapenest/download-service              (/usr/local/bin/download-service)
#   Worker: docker run tapenest/download-service download-worker
# The worker needs yt-dlp (+ Python), ffmpeg (muxing) and deno (yt-dlp's JS
# runtime for YouTube signature challenges), so the runtime is Debian slim
# rather than distroless (deno ships glibc binaries only).
FROM golang:1.22.12-alpine3.21@sha256:1699c10032ca2582ec89a24a1312d986a3f094aed3d5c1147b19880afe40e052 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/download-service ./cmd/server && \
    go build -ldflags="-s -w" -o /out/download-worker ./cmd/worker

FROM denoland/deno:bin-2.9.7@sha256:bc5aa4466e21b6d3021226a85ba2e1911f7c386254d97b9d797903ab74edace2 AS deno

FROM debian:12-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
# hadolint ignore=DL3008
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates tzdata python3 ffmpeg && \
    rm -rf /var/lib/apt/lists/* && \
    useradd --system --uid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin tapenest && \
    install -d -o 10001 -g 10001 /var/tmp/tapenest-dl
# yt-dlp release zipapp pinned by checksum (update together with the version)
ADD --chmod=0755 --checksum=sha256:1fa6733c37ea6fb51c99ad8fe785e7b7e5f3246c9b980230329d4fb72ed8d4d6 \
    https://github.com/yt-dlp/yt-dlp/releases/download/2026.08.19/yt-dlp /usr/local/bin/yt-dlp
COPY --from=deno /deno /usr/local/bin/deno
COPY --from=build /out/download-service /out/download-worker /usr/local/bin/
ENV YTDLP_PATH=/usr/local/bin/yt-dlp YTDLP_JS_RUNTIMES=deno DOWNLOAD_TMP_DIR=/var/tmp/tapenest-dl
USER 10001:10001
EXPOSE 8082 8083
ENTRYPOINT []
CMD ["download-service"]
