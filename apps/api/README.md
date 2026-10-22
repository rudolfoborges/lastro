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
| `pnpm nx run api:vulncheck` | Runs `govulncheck` against the code and the Go toolchain |
| `pnpm nx run api:migrate up` | Applies pending migrations (`down` rolls back one, `status` lists them) |
| `pnpm nx run api:sqlc` | Regenerates the query code from `internal/store/queries` |
| `pnpm nx run api:sqlc-check` | Fails when the generated code is out of date (runs in CI) |

`build`, `test` and `lint` are cached by Nx; the cache key includes the Go toolchain version.

Go tools used by the targets are pinned in `tools.mod` and run with `go tool -modfile=tools.mod <tool>`, so local runs and CI use the same versions without adding the tools' dependencies to `go.mod`. The Go toolchain itself is pinned by the `toolchain` directive in `go.mod`.

To run locally, copy `.env.example` to `.env` and adjust `DATABASE_URL`.

## Binary

```
lastro <command> [arguments]
```

| Command | What it does |
| --- | --- |
| `serve` | Starts the HTTP API and shuts down gracefully on `SIGINT`/`SIGTERM` |
| `migrate up\|down\|status` | Applies every pending migration, rolls back the latest one or lists them |
| `invites create [--expires-in 168h] [--note text]` | Creates a single-use sign-up invite and prints its code once |
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
| `RATE_LIMIT_PER_MINUTE` | no | `120` | Requests per minute for each API key |
| `UNAUTHENTICATED_RATE_LIMIT_PER_MINUTE` | no | `10` | Sign-ups and failed authentications per minute for each client IP |
| `TRUST_PROXY_HEADERS` | no | `false` | Read the client IP from `X-Real-IP`/`X-Forwarded-For`; enable only behind a proxy that overwrites them |

## HTTP endpoints

| Route | Auth | Purpose |
| --- | --- | --- |
| `GET /healthz` | none | Liveness; never touches the database |
| `GET /readyz` | none | Readiness; `503` when Postgres does not answer within 2s |
| `POST /v1/accounts` | invite code | Creates an account and returns its first `admin` key |
| `GET /v1/whoami` | any key | Describes the account and the calling key |
| `GET /v1/api-keys` | `admin` | Lists the account's keys, never their tokens |
| `POST /v1/api-keys` | `admin` | Creates a key with the given scopes and returns its token once |
| `POST /v1/api-keys/{id}/revoke` | `admin` | Revokes a key immediately; the last active `admin` key cannot be revoked |

Errors are JSON objects such as `{"error": "unauthorized"}`, with an optional `message`.

### Accounts and API keys

1. The operator creates an invite: `lastro invites create --note "for Rudolfo"`. Invites are single use and expire (7 days by default).
2. Whoever holds the code signs up:

   ```bash
   curl -X POST https://<host>/v1/accounts \
     -d '{"invite_code": "inv_…", "name": "Rudolfo"}'
   ```

   The response carries an `admin` key. Store it safely: the token is shown only once.
3. The admin key creates a key for each client with only the scopes it needs, for example `expenses:read` and `expenses:write` for Case and `wallet:ingest` for the Android app:

   ```bash
   curl -X POST https://<host>/v1/api-keys \
     -H "Authorization: Bearer lst_…" \
     -d '{"name": "case", "scopes": ["expenses:read", "expenses:write"]}'
   ```

Clients send `Authorization: Bearer <token>`. Scopes are `admin`, `expenses:read`, `expenses:write`, `wallet:ingest` and `statements:write`.

Tokens look like `lst_<key id>_<secret>`. Only the SHA-256 of the 32-byte secret is stored and it is compared in constant time; revoked keys fail on the next request. Requests without a valid token get `401` and requests without the scope get `403`, both without details. Each key and each client IP (for sign-ups and failed authentications) are rate limited and get `429` with `Retry-After`.

Every authenticated request is audited in `api_requests` (key, method, route, status, duration and request ID, never headers or bodies) and refreshes the key's `last_used_at`.

Every response carries an `X-Request-ID` header. A valid incoming ID is kept; otherwise a UUIDv7 is generated. Access logs record method, route, status, duration and request ID, never headers or bodies.

## Layout

| Path | Contents |
| --- | --- |
| `cmd/lastro` | Entry point |
| `internal/cli` | Subcommands |
| `internal/accounts` | Accounts, invites, API keys, scopes and request audit |
| `internal/config` | Typed configuration loaded from the environment |
| `internal/httpapi` | Routes, handlers and middleware |
| `internal/logging` | `log/slog` setup |
| `internal/store` | Postgres pool and the platform and tenant transactions |
| `internal/store/migrations` | SQL migrations embedded in the binary and the goose runner |
| `internal/store/queries/{platform,tenant}` | SQL queries compiled by sqlc |
| `internal/store/{platformdb,tenantdb}` | Code generated by sqlc (versioned; do not edit) |
| `internal/store/storetest` | Isolated databases for integration tests |

## Database

Postgres is the only infrastructure dependency. Access uses `pgx/v5` with code generated by `sqlc`; schema changes are SQL migrations run by `goose` and embedded in the binary.

### Conventions

- **IDs** are UUIDv7 generated by the application (`store.NewID()`), never by the database.
- **Timestamps** are `timestamptz`. Connections use the `UTC` time zone.
- **Money** is a `bigint` amount in minor units (cents) plus a `char(3)` ISO 4217 currency. No floating point anywhere.
- **Enumerations** are `text` columns with a `CHECK` constraint, which keeps them easy to evolve.
- **No physical `DELETE`** on domain tables: changes are recorded through status columns. The tenant role has no `DELETE` privilege and a test fails if a query file contains `DELETE FROM`.
- **Migrations** are named `NNNNN_description.sql`, always have a `-- +goose Down` section and must run up and down cleanly on an empty database.

### Multi-tenancy

Every account's data is isolated in two layers:

1. Every tenant-owned table has an `account_id uuid NOT NULL REFERENCES accounts (id)` column, and every tenant query filters by it.
2. Row-level security is the second barrier. Each tenant-owned table enables RLS with a policy such as:

   ```sql
   ALTER TABLE things ENABLE ROW LEVEL SECURITY;
   CREATE POLICY tenant_isolation ON things USING (account_id = current_account_id());
   ```

Queries are split in two sqlc packages:

| Package | Runs as | Use for |
| --- | --- | --- |
| `tenantdb`, via `Store.TenantTx(ctx, accountID, fn)` | `lastro_app`, with `app.account_id` set for the transaction | Everything done on behalf of an account |
| `platformdb`, via `Store.Platform()` or `Store.PlatformTx` | The schema owner, which bypasses RLS | Only what happens before an account is known, such as API key lookup and sign-up |

`TenantTx` runs `SET LOCAL ROLE lastro_app` and `set_config('app.account_id', …, true)` at the start of the transaction. The role is created by the first migration with `SELECT`, `INSERT` and `UPDATE` as default privileges on new tables; it has no `DELETE` and does not bypass RLS.

### Integration tests

Tests that touch Postgres use `storetest.MigratedDatabaseURL(t)` or `storetest.EmptyDatabaseURL(t)`, which create a uniquely named database for the test and drop it afterwards. Migrated databases are cloned from a template built once per migration set, so each test starts from a clean schema in milliseconds.

They need `TEST_DATABASE_URL` pointing at a Postgres server where the user can create databases (the `lastro_test` database of the local compose or the CI service). Without it the tests are skipped locally and fail in CI.

## Lint conventions

`.golangci.yml` follows the repository conventions: code and comments in English (`misspell` with the US locale) and no rule that demands doc comments (`staticcheck` ST1000 and ST1020–ST1022 are disabled, and `revive` is not enabled).
