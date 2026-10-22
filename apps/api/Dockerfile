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

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/lastro /usr/local/bin/lastro
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["lastro"]
CMD ["serve"]
