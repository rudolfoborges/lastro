# api

Go service that stores, validates and computes Lastro's data. Case reaches it over MCP and the Android app sends wallet notifications over HTTP. The service never interprets or classifies anything; see the architecture principle in the root `CLAUDE.md`.

## Requirements

- Go, at the version declared in `go.mod`
- [golangci-lint](https://golangci-lint.run/) v2
- Node.js and pnpm, because tasks run through Nx

## Commands

Run every task through Nx from the repository root:

| Command | What it does |
| --- | --- |
| `pnpm nx run api:serve` | Runs the API with `go run`, loading `apps/api/.env` |
| `pnpm nx run api:build` | Builds the `lastro` binary into `dist/apps/api/` |
| `pnpm nx run api:test` | Runs the tests with the race detector |
| `pnpm nx run api:lint` | Runs golangci-lint with `.golangci.yml` |
| `pnpm nx run api:tidy` | Runs `go mod tidy` |

`build`, `test` and `lint` are cached by Nx; the cache key includes the Go toolchain version.

To run locally, copy `.env.example` to `.env` and adjust `DATABASE_URL`.

## Binary

```
lastro <command> [arguments]
```

| Command | What it does |
| --- | --- |
| `serve` | Starts the HTTP API and shuts down gracefully on `SIGINT`/`SIGTERM` |
| `help` | Lists the commands |

## Configuration

Configuration comes only from environment variables. The process refuses to boot and lists every invalid or missing variable.

| Variable | Required | Default | Notes |
| --- | --- | --- | --- |
| `DATABASE_URL` | yes | | Postgres connection string |
| `LASTRO_ENV` | no | `development` | `development`, `test` or `production` |
| `PORT` | no | `8080` | HTTP port |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn` or `error` |
| `LOG_FORMAT` | no | `json` in production, `text` otherwise | `json` or `text` |
| `SHUTDOWN_TIMEOUT` | no | `15s` | Time allowed for in-flight requests on shutdown |

## HTTP endpoints

| Route | Purpose |
| --- | --- |
| `GET /healthz` | Liveness; never touches the database |
| `GET /readyz` | Readiness; `503` when Postgres does not answer within 2s |

Every response carries an `X-Request-ID` header. A valid incoming ID is kept; otherwise a UUIDv7 is generated. Access logs record method, route, status, duration and request ID, never headers or bodies.

## Layout

| Path | Contents |
| --- | --- |
| `cmd/lastro` | Entry point |
| `internal/cli` | Subcommands |
| `internal/config` | Typed configuration loaded from the environment |
| `internal/httpapi` | Routes, handlers and middleware |
| `internal/logging` | `log/slog` setup |
| `internal/store` | Postgres access |

## Lint conventions

`.golangci.yml` follows the repository conventions: code and comments in English (`misspell` with the US locale) and no rule that demands doc comments (`staticcheck` ST1000 and ST1020–ST1022 are disabled, and `revive` is not enabled).
