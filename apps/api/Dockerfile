# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.10

FROM golang:${GO_VERSION}-alpine AS base
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

FROM base AS dev
RUN --mount=type=cache,target=/go/pkg/mod go install github.com/air-verse/air@v1.67.4
COPY . .
CMD ["air", "-c", ".air.toml"]

FROM base AS build
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/lastro ./cmd/lastro

# Alpine instead of distroless: Railway runs pre-deploy commands and `railway ssh`
# sessions (needed for `lastro invites create`) inside this image, and both
# need a shell and the binary on PATH.
FROM alpine:3.24 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 lastro
COPY --from=build /out/lastro /usr/local/bin/lastro
USER lastro
EXPOSE 8080
CMD ["lastro", "serve"]
