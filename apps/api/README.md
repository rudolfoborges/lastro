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
| `pnpm nx run api:seed` | Runs `lastro seed` against `DATABASE_URL` |

`build`, `test` and `lint` are cached by Nx; the cache key includes the Go toolchain version.

Go tools used by the targets are pinned in `tools.mod` and run with `go tool -modfile=tools.mod <tool>`, so local runs and CI use the same versions without adding the tools' dependencies to `go.mod`. The Go toolchain itself is pinned by the `toolchain` directive in `go.mod`.

To run locally, copy `.env.example` to `.env` and adjust `DATABASE_URL`, or use the compose environment described in the root README.

## Container image

`Dockerfile` has a `dev` stage (Go toolchain and air, used by compose) and a `runtime` stage: the static binary on Alpine, running as the unprivileged `lastro` user with `lastro serve` as the default command. Alpine rather than distroless because Railway's pre-deploy command and `railway ssh` sessions need a shell and `lastro` on `PATH`. Deployment is described in [`docs/operations.md`](../../docs/operations.md).

## Binary

```
lastro <command> [arguments]
```

| Command | What it does |
| --- | --- |
| `serve` | Starts the HTTP API and the MCP server on `/mcp`, and shuts down gracefully on `SIGINT`/`SIGTERM` |
| `mcp-stdio` | Serves MCP over stdin/stdout as the account of `LASTRO_API_KEY`, for local MCP clients; logs go to stderr |
| `migrate up\|down\|status` | Applies every pending migration, rolls back the latest one or lists them |
| `invites create [--expires-in 168h] [--note text]` | Creates a single-use sign-up invite and prints its code once |
| `seed` | Creates a development account with `admin`, `case` and `android` keys and prints their tokens; refuses to run in production |
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
| `/mcp` | any key | MCP over Streamable HTTP (stateless, JSON responses); each tool checks its own scope |
| `GET /healthz` | none | Liveness; never touches the database |
| `GET /readyz` | none | Readiness; `503` when Postgres does not answer within 2s |
| `POST /v1/accounts` | invite code | Creates an account and returns its first `admin` key |
| `GET /v1/whoami` | any key | Describes the account and the calling key |
| `PATCH /v1/account` | `admin` | Changes the account settings: `{"timezone": "America/Sao_Paulo"}` |
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
| `internal/accounts` | Accounts, invites, API keys, scopes, account settings and request audit |
| `internal/calendar` | Local dates, date precision and source, `now`/`today`/`yesterday` and time zones |
| `internal/redaction` | Masking of card numbers and CPFs and removal of codes, passwords and tokens in payloads and logs |
| `internal/mcpserver` | MCP server: tools, prompts, server instructions and Case's procedures (`instructions/*.md`) |
| `internal/money` | Amounts in minor units: canonical parsing, pt-BR formatting and same-currency arithmetic |
| `internal/config` | Typed configuration loaded from the environment |
| `internal/httpapi` | Routes, handlers and middleware |
| `internal/idempotency` | Exactly-once execution of client submissions, keyed by account, operation and client key |
| `internal/ledger` | Domain core: observations and their interpretations, transactions, evidence links, payment methods and field provenance |
| `internal/logging` | `log/slog` setup |
| `internal/taxonomy` | The default categories every account starts with |
| `internal/store` | Postgres pool and the platform and tenant transactions |
| `internal/store/migrations` | SQL migrations embedded in the binary and the goose runner |
| `internal/store/queries/{platform,tenant}` | SQL queries compiled by sqlc |
| `internal/store/{platformdb,tenantdb}` | Code generated by sqlc (versioned; do not edit) |
| `internal/store/storetest` | Isolated databases for integration tests |
| `docs/data-model.md` | Entity-relationship diagram and the rules the schema enforces |

## Database

Postgres is the only infrastructure dependency. Access uses `pgx/v5` with code generated by `sqlc`; schema changes are SQL migrations run by `goose` and embedded in the binary.

### Conventions

- **IDs** are UUIDv7 generated by the application (`store.NewID()`), never by the database.
- **Timestamps** are `timestamptz`. Connections use the `UTC` time zone.
- **Money** is a `bigint` amount in minor units (cents) plus a `char(3)` ISO 4217 currency. No floating point anywhere. Clients send amounts as canonical decimal strings (`"327.39"`); the server rejects any other format, such as `"R$ 327,39"` or `"1.200,00"`, instead of guessing, and formats pt-BR text (`R$ 327,39`) only for messages relayed to the user.
- **Dates** keep their precision (`exact`, `day`, `month`, `unknown`) and source (`stated`, `inferred`, `document`). Local dates are `date` columns; `now`, `today` and `yesterday` are resolved from the message time in the account time zone (`accounts.timezone`, `America/Sao_Paulo` by default) and marked inferred.
- **Enumerations** are `text` columns with a `CHECK` constraint, which keeps them easy to evolve.
- **No physical `DELETE`** on domain tables: changes are recorded through status columns. The tenant role has no `DELETE` privilege and a test fails if a query file contains `DELETE FROM`.
- **Tenant references** between tables use composite foreign keys on `(account_id, id)`, so a row cannot point at another account's data.
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

### Corrections and history

Every change to a transaction goes through `ledger.Service.Correct` (or `Void`), which writes one row per changed field to the append-only `revisions` table: old and new value, the mandatory reason, the author (`user`, `agent`, `rule` or `system`), the credential that acted and, for automatic changes, the observation behind them. The transaction's creation is the first revision, so `History` tells the whole story in order.

Fields Rudolfo changed are marked `confirmed_by_user` in `field_provenance`. Later changes by Case, rules or the system (a new statement, a notification, a reconciliation) never overwrite them: `Correct` applies the other fields and returns the locked ones as conflicts, which become clarifications. The original value always stays in the observation.

Changing the payment method re-applies the status rule: credit card purchases stay `provisional` until a statement line confirms them; Pix, debit card and cash reported by Rudolfo are `confirmed`; an unknown method is `provisional`. A voided transaction is kept with status `voided` and cannot change anymore.

### Categories

Every account starts with the categories of the PRD (`internal/taxonomy`); codes are English (`groceries`, `convenience_snacks`…) and labels are pt-BR display data. Categories can be created, renamed and archived per account; `unidentified` cannot be archived.

A transaction's categories are `category_allocations`: one or more parts whose amounts add up **exactly** to the transaction amount, each `suggested` (by Case, origin `agent`, or by a rule, origin `rule`) or `confirmed` (only by Rudolfo, origin `user`). Replacing them supersedes the old rows, which stay for history, and writes a `category` revision. The service rejects a split that does not match to the cent with the totals in the message, and deferred constraint triggers enforce the same rule at commit. Confirmed categories are locked against Case and rules: a different suggestion comes back as a conflict. When the amount is corrected, a single category follows it; a split must be sent again with the new amount. A transaction without allocations counts in full as "Não identificado" (`EffectiveAllocations`).

### Recording from the conversation

`record_expense` (`ledger.Service.RecordExpense`) turns what Case understood into an observation `conversation` (raw text and Case's reading, redacted), its interpretation, a transaction with the conversation as confirmed evidence, the category Case gave and the questions that remain, all in one transaction:

| `result` | `outcome` | Meaning |
| --- | --- | --- |
| `recorded` | `completed` | Written; the answer lists `inferred` and `pending` fields |
| `duplicate_of` | `completed` | The same `request_id` was already recorded; the existing transaction is returned |
| `possible_duplicate` | `pending` | Nothing written: the same amount and normalized text arrived in the same ChatGPT conversation (`_meta["openai/session"]`) in the last 24 hours. Case asks Rudolfo and, for a second real purchase, repeats the call with `confirm_distinct: true` |
| `needs_clarification` | `pending` | Nothing written: the amount or what was bought is missing; `question` says what to ask |

Partial records are accepted: without a payment method the transaction stays `provisional`; without a category it counts as "Não identificado" and opens a question. `date_reference` (`now`, `today`, `yesterday`) is resolved from `message_at` (or the moment of the call) in the account time zone and marked inferred. Installments are kept as informed; the installment plan comes later.

### Clarifications

Questions for Rudolfo are rows in `clarifications` (kind, target transaction or observation, question in pt-BR, context, `open`/`resolved`/`dismissed`). The ledger opens and closes them as a side effect of every change, idempotently: the same cause never has two open questions (`dedup_key`), and a dismissed question is not asked again for the same cause.

- `category_unknown`: a transaction without categories; closed when it gets one.
- `purpose_unknown`: a Pix without purpose; closed when the purpose is set.
- `field_conflict`: Case, a rule or the system tried to change a field Rudolfo confirmed; the context carries the current and proposed values, and it closes when Rudolfo changes that field himself.
- Voiding a transaction dismisses its open questions.

Resolving needs the missing data (categories or purpose), which is applied as Rudolfo's correction; dismissing changes nothing. Both are written to the transaction history. The other kinds (`possible_match`, `reading_doubt`, `statement_*`…) are opened by the notification, statement and reconciliation features.

### Idempotency

Every write submitted by a client carries a key chosen by the client (for Case, the `request_id` it generates once per expense and reuses on every retry). `idempotency.Do` runs inside the same tenant transaction as the write:

- same key and same request: the stored response is replayed and nothing is written again;
- same key and a different request: `idempotency.ErrConflict`;
- concurrent calls with the same key wait on a transaction-scoped advisory lock and then replay the first response;
- if the work fails, the transaction rolls back and no key is recorded, so the retry runs from scratch.

A key is therefore only ever stored for work that was committed, and a response is never sent for a write that did not happen. Keys are kept: the table has no `UPDATE` or `DELETE` for the application role.

### Integration tests

Tests that touch Postgres use `storetest.MigratedDatabaseURL(t)` or `storetest.EmptyDatabaseURL(t)`, which create a uniquely named database for the test and drop it afterwards. Migrated databases are cloned from a template built once per migration set, so each test starts from a clean schema in milliseconds.

They need `TEST_DATABASE_URL` pointing at a Postgres server where the user can create databases (the `lastro_test` database of the local compose or the CI service). Without it the tests are skipped locally and fail in CI.

## MCP server

Case reaches Lastro through MCP, built on the official Go SDK. `/mcp` serves Streamable HTTP in stateless mode with JSON responses, so every request is authenticated on its own and any instance can answer it; `lastro mcp-stdio` serves the same tools over stdio for local clients.

- **Authentication**: `/mcp` goes through the same bearer authentication, rate limits and request audit as `/v1`. Each tool requires a scope (`expenses:read` for reads, `expenses:write` for writes) and refuses the call with a `forbidden` tool error otherwise.
- **The server never interprets**: tools receive data Case already understood, validate it and answer with errors Case can act on. A tool error carries `{"outcome": "failed", "error": code, "field", "message"}` and means nothing was written.
- **Conventions**: `snake_case` names, English descriptions and schemas, JSON Schema input and structured output, annotations (`readOnlyHint`, `idempotentHint`, `destructiveHint`) on every tool, and a `summary` in pt-BR on writes for Case to relay.
- **Procedures**: Case's procedures live in `internal/mcpserver/instructions/` and are served by the `get_instructions` tool (the channel ChatGPT uses) and as MCP prompts (`conversation_procedure`, `classification_policy`) for other clients. The server `instructions` summarize the rules.
- **Audit**: every tool call is recorded in `tool_calls` (tool, credential, outcome, error code, duration), never arguments or results.
- **Conversation identity**: the `openai/session` value ChatGPT sends in `_meta` is read by the server for the repetition warning of `record_expense`; it is not a tool argument.

| Tool | Scope | What it does |
| --- | --- | --- |
| `get_instructions` | any | Returns a procedure (`conversation`, `classification`) in Markdown |
| `list_categories` | `expenses:read` | Lists the account's category codes and labels |
| `save_category` | `expenses:write` | Creates a category or renames an existing code |
| `record_expense` | `expenses:write` | Records an expense Case understood from the conversation |
| `list_clarifications` | `expenses:read` | Lists pending questions by kind, status and purchase period |
| `resolve_clarification` | `expenses:write` | Answers (`resolve`) or closes (`dismiss`) a question |

Every write tool takes a `request_id` that Case generates once per intention and reuses on retries; the ledger runs it through `idempotency.Do` in the same transaction.

## Sensitive data

Every free-text value that reaches the database (conversation text, notification text, statement excerpts sent by Case) goes through `redaction.Text` or `redaction.Values` before it is persisted, and every log line goes through `redaction.NewHandler`:

- card numbers (13 to 19 digits, optionally grouped by spaces or hyphens, with a valid Luhn check digit) keep only the last four digits;
- valid CPFs are masked completely;
- authentication codes, OTPs, CVVs, passwords and tokens (labelled values such as `código: 482913`, `Bearer …`, `lst_…` keys and JWT-like strings) are replaced by `[redacted]`.

The redaction returns what it found as `{kind, count}` pairs, which are stored next to the evidence for audit; the redacted values themselves are never kept. Only Google Wallet notifications (`redaction.AllowedNotificationPackage`) are accepted from the Android app.

## Lint conventions

`.golangci.yml` follows the repository conventions: code and comments in English (`misspell` with the US locale) and no rule that demands doc comments (`staticcheck` ST1000 and ST1020–ST1022 are disabled, and `revive` is not enabled).
