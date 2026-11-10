# syntax=docker/dockerfile:1
FROM golang:1.27.3-trixie AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out && ./scripts/fetch-assets.sh && go mod tidy && go test ./... && CGO_ENABLED=0 go build -ldflags "-X vartovyi/internal/buildinfo.Version=${VERSION}" -o /out/ ./cmd/...
RUN CGO_ENABLED=0 go test -c -o /out/integration.test ./integration

FROM bluenviron/mediamtx:1.21.3 AS upstream-media

FROM debian:trixie-slim AS runtime
RUN apt-get update && apt-get install --no-install-recommends -y ca-certificates ffmpeg && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home vartovyi
COPY --from=build /out/ /usr/local/bin/
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/api"]

FROM runtime AS media
COPY --from=upstream-media /mediamtx /usr/local/bin/mediamtx
ENTRYPOINT ["/usr/local/bin/mediamtx"]
