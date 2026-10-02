# syntax=docker/dockerfile:1.7
# acquisition-service image (ADR 0011): one image, two entrypoints.
#   API:    docker run tapenest/acquisition-service
#   Worker: docker run --entrypoint /acquisition-worker tapenest/acquisition-service
FROM golang:1.22.12-alpine3.21@sha256:1699c10032ca2582ec89a24a1312d986a3f094aed3d5c1147b19880afe40e052 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOTOOLCHAIN=local GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/acquisition-service ./cmd/server && \
    go build -ldflags="-s -w" -o /out/acquisition-worker ./cmd/worker

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/acquisition-service /out/acquisition-worker /
USER 65532:65532
EXPOSE 8088 8089
ENTRYPOINT ["/acquisition-service"]
