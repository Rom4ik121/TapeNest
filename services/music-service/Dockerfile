# syntax=docker/dockerfile:1.7
# music-service image (spec stage 3): one image, two entrypoints.
#   docker build -t tapenest/music-service services/music-service
#   API:    docker run tapenest/music-service                        (/music-service)
#   Worker: docker run --entrypoint /music-worker tapenest/music-service
# Pure Go (streams are proxied from Navidrome, no transcoding) → distroless.
FROM golang:1.22.12-alpine3.21@sha256:1699c10032ca2582ec89a24a1312d986a3f094aed3d5c1147b19880afe40e052 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/music-service ./cmd/server && \
    go build -ldflags="-s -w" -o /out/music-worker ./cmd/worker

# distroless: no shell, CA certs + tzdata included, runs as uid 65532
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/music-service /out/music-worker /
USER 65532:65532
EXPOSE 8084 8085
ENTRYPOINT ["/music-service"]
