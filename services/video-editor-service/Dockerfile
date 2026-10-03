# syntax=docker/dockerfile:1.7
# video-editor-service: API :8096 and worker :8097. ffmpeg edits the user's own download.
FROM golang:1.22.12-alpine3.21@sha256:1699c10032ca2582ec89a24a1312d986a3f094aed3d5c1147b19880afe40e052 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/video-editor-service ./cmd/server && \
    go build -ldflags="-s -w" -o /out/video-editor-worker ./cmd/worker

FROM debian:12-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
# hadolint ignore=DL3008
RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates tzdata ffmpeg fonts-dejavu-core && \
    rm -rf /var/lib/apt/lists/* && \
    useradd --system --uid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin tapenest && \
    install -d -o 10001 -g 10001 /var/tmp/tapenest-edit
COPY --from=build /out/video-editor-service /out/video-editor-worker /usr/local/bin/
ENV VIDEO_EDITOR_TMP=/var/tmp/tapenest-edit \
    VIDEO_FONT_FILE=/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf
USER 10001:10001
EXPOSE 8096 8097
ENTRYPOINT []
CMD ["video-editor-service"]
