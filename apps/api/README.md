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
| `accounts list` | Lists accounts with ID, creation time and name (operator only: it reads across accounts) |
| `keys create --account <id> --name <name> --scopes <a,b>` | Issues an API key for an account and prints its token once, for example to replace a lost `admin` key |
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
| `PUBLIC_BASE_URL` | in production | `http://localhost:$PORT` | Public origin (`https://lastro.code04.dev`): the OAuth issuer, and `$PUBLIC_BASE_URL/mcp` is the resource tokens are bound to |
| `OAUTH_REDIRECT_HOSTS` | no | `chatgpt.com` | Hosts OAuth clients may redirect to (HTTPS only; `localhost` is also accepted outside production) |
| `OAUTH_METADATA_HOSTS` | no | `chatgpt.com` | Hosts allowed to serve client ID metadata documents (CIMD) |

## HTTP endpoints

| Route | Auth | Purpose |
| --- | --- | --- |
| `/mcp` | any key or OAuth token | MCP over Streamable HTTP (stateless, JSON responses); each tool checks its own scope |
| `GET /.well-known/oauth-protected-resource` | none | Protected resource metadata for `/mcp` (RFC 9728) |
| `GET /.well-known/oauth-authorization-server` | none | Authorization server metadata (RFC 8414) |
| `POST /oauth/register` | none | Dynamic client registration (RFC 7591) |
| `GET`/`POST /oauth/authorize` | admin key in the form | Consent page and decision (authorization code + PKCE S256) |
| `POST /oauth/token` | client | Authorization code and refresh token grants |
| `POST /oauth/revoke` | client | Revokes the grant of a token (RFC 7009) |
| `GET /v1/oauth/grants` | `admin` | Lists the clients authorized on the account |
| `POST /v1/oauth/grants/{id}/revoke` | `admin` | Revokes a client's access immediately |
| `GET /healthz` | none | Liveness; never touches the database |
| `GET /readyz` | none | Readiness; `503` when Postgres does not answer within 2s |
| `POST /v1/accounts` | invite code | Creates an account and returns its first `admin` key |
| `GET /v1/whoami` | any key | Describes the account and the calling key |
| `PATCH /v1/account` | `admin` | Changes the account settings: `{"timezone": "America/Sao_Paulo"}` |
| `GET /v1/api-keys` | `admin` | Lists the account's keys, never their tokens |
| `POST /v1/api-keys` | `admin` | Creates a key with the given scopes and returns its token once |
| `POST /v1/api-keys/{id}/revoke` | `admin` | Revokes a key immediately, together with the OAuth grants it authorized; the last active `admin` key cannot be revoked |
| `POST /v1/wallet/notifications` | `wallet:ingest` | Receives one notification event from the Android app (see [Wallet notifications](#wallet-notifications)) |
| `GET /v1/wallet/notifications` | `wallet:ingest` | The latest notification versions of one device and what Case did with each (see [What the app sees](#what-the-app-sees)) |
| `POST /v1/wallet/heartbeat` | `wallet:ingest` | Sign of life from the Android app with its version and permissions; answers the tracking state (see [Coverage](#coverage)) |

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

Each transaction has **one category**, stored in `category_allocations` as a single current row covering the whole amount. It is `suggested` (by Case, origin `agent`, or by a rule, origin `rule`) or `confirmed` (only by Rudolfo, origin `user`). Changing it supersedes the old row, which stays for history, and writes a `category` revision. The database keeps at most one current category per transaction (`category_allocations_one_current_idx`), and deferred constraint triggers refuse at commit a category whose amount differs from the transaction's. Confirmed categories are locked against Case and rules: a different suggestion comes back as a conflict. When the amount is corrected, the category follows it. A transaction without a category counts in full as "Não identificado" (`EffectiveCategory`). Splitting a purchase between categories was left out of the first delivery (#83, D8).

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

### Wallet notifications

The Android app sends each notification event to `POST /v1/wallet/notifications` with an API key scoped `wallet:ingest`:

```json
{
  "device_id": "galaxy-s24",
  "package": "com.google.android.apps.walletnfcrel",
  "notification_key": "0|com.google.android.apps.walletnfcrel|7|null|10123",
  "event": "posted",
  "posted_at": "2026-10-22T15:04:05Z",
  "title": "…",
  "text": "…",
  "big_text": "…",
  "sent_at": "2026-10-22T15:04:25Z"
}
```

- `sent_at` is the device time when the request is sent, accepted only within 5 minutes of the server time (`400 stale_request` otherwise). There is no HMAC: the key is the credential. A retry sends a fresh `sent_at`; the rest of the body stays the same.
- While automatic tracking is not active (it starts disabled and is turned on with `set_tracking`), the answer is `200 tracking_paused` and nothing is stored, not even the device.
- Notifications from packages other than Google Wallet are not stored: the answer is `discarded` and only `wallet_devices.discarded_count` grows.
- Title, text and big text are redacted before anything is persisted, and each version becomes an immutable observation `wallet_notification` pending interpretation by Case. The server never reads the text.
- Identity is `(device_id, notification_key)` plus a hash of the redacted package, title, texts and `posted_at`. The same content again answers `200 duplicate` and writes nothing; different content (an update, or another post under a reused key) answers `201 stored` with the next `version` of the same notification.
- `removed` only records `removed_at` on the notification. It is never treated as a cancellation and changes no transaction; a removal of a notification the server never stored answers `unknown_notification`.

### Interpreting notifications

A notification version is pending while its observation has no interpretation. Case lists them with `list_pending_notifications` (oldest first, keyset cursor, earlier versions of the same notification with Case's reading of each) and sends its reading back with `submit_notification_interpretations` (`ledger.Service.InterpretNotification`). Lastro applies each reading with the PRD rules as invariants:

| `event_type` | Effect |
| --- | --- |
| `purchase` | A new transaction (`created`), with the status rule of the payment method. With `related_transaction_id` it confirms a record from the conversation instead (`linked`): the notification becomes its evidence and fills only what is missing (card, exact time, merchant), plus the amount when it differs in the same currency |
| `purchase_update` | Corrects the transaction of an earlier version of the same notification (`updated`); never creates one |
| `cancellation` | Voids the transaction (`voided`), unless a statement line already charges it |
| `refund` | A separate `refund` transaction pointing at the purchase (`refund_of_id`), with its category; refunds never exceed the purchase |
| `declined`, `notice`, `unrelated` | Nothing (`none`) |
| `unknown` | Nothing, plus a `reading_doubt` clarification on the observation |

- **Idempotency:** each interpretation stores a hash of the submitted reading. The same reading again answers `already_applied`. A different one is refused (`already_interpreted`) unless Case sends `reinterpret` with a reason; then a purchase reading corrects the same transaction, and a non-expense reading undoes the evidence and voids the transaction the notification created. Other changes are refused (`reinterpretation_not_supported`).
- **Possible matches:** a `purchase` without `related_transaction_id` is checked against transactions of earlier versions of the same notification and against open purchases with the same amount and currency within two days that no notification confirms yet. If there are any, nothing is written and the answer is `possible_match` with the candidates and the reasons in pt-BR. Case decides: `related_transaction_id` for the same purchase, `confirm_distinct` for another.
- **Invariants:** a transaction confirmed by one notification cannot be confirmed by another, a cancellation cannot void a charged purchase, and refunds cannot add up to more than the purchase.

### What the app sees

`GET /v1/wallet/notifications?device_id=galaxy-s24&limit=50` (`wallet:ingest`, `limit` 1 to 100, 50 by default) lists the latest versions received from that device, newest first, so the app can show whether Case already processed each one:

```json
{
  "notifications": [
    {
      "notification_id": "…",
      "notification_key": "0|com.google.android.apps.walletnfcrel|7|null|10123",
      "version": 1,
      "event": "posted",
      "posted_at": "2026-10-22T15:04:05Z",
      "received_at": "2026-10-22T15:04:25Z",
      "status": "interpreted",
      "interpretation": {
        "event_type": "purchase",
        "effect": "created",
        "transaction": {
          "status": "provisional",
          "category": { "code": "meals", "label": "Alimentação e refeições", "status": "suggested" }
        }
      }
    }
  ]
}
```

- `status` is `pending_interpretation` until Case submits a reading, then `interpreted` with the latest reading (`event_type`, `effect`) and the current status and category of the transaction it touched.
- The text never comes back: the app already keeps it, and a leaked device key reveals no more than which notifications Case processed and how they were classified.

### Coverage

Missing notifications never mean there was no spending, so Lastro tells how complete the automatic tracking was:

- **Tracking periods** (`wallet_tracking_periods`): Rudolfo turns tracking on, pauses and resumes it through Case (`set_tracking`). It starts disabled. Each period also keeps the inbox interval: how often Rudolfo's routine processes the inbox (6 h by default).
- **Heartbeats** (`wallet_heartbeats`, append-only): the app calls `POST /v1/wallet/heartbeat` every 15 minutes with `device_id`, `client_version`, `notification_access`, `battery_unrestricted`, `queue_size` and `sent_at` (same 5-minute window). The answer carries the tracking state, so the app can show it.
- **Computed at query time**, no background job (`ledger.Service.Coverage`, MCP `get_coverage_status`):
  - a gap is a stretch longer than 60 minutes without a heartbeat while tracking is active. Turning tracking on counts as a sign of life, and a gap that is still going on shows up 60 minutes after the last heartbeat;
  - paused time is reported as inactive, never as a gap;
  - a late delivery is a notification received more than 15 minutes after `posted_at`;
  - the inbox is late when its oldest pending notification waits more than twice the inbox interval. The last time the inbox was processed is the last completed `list_pending_notifications` call in `tool_calls`;
  - the coverage is `complete` only when tracking was active for the whole period, with no gaps, notification access and nothing pending. Every limitation comes as a pt-BR sentence for Case to relay before any total of that period (PRD criterion 11).

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

## OAuth for Case

ChatGPT, where Case runs, only connects to MCP servers through OAuth 2.1, so Lastro is its own authorization server (`internal/oauth`). There is no password: Rudolfo proves the account is his by pasting his `admin` API key on the consent page.

1. ChatGPT calls `/mcp`, gets `401` with `WWW-Authenticate: Bearer …, resource_metadata="…/.well-known/oauth-protected-resource"` and discovers the authorization server.
2. It identifies itself with a **client ID metadata document** (CIMD, `client_id=https://chatgpt.com/oauth/client.json`), fetched over HTTPS from an allowed host with a timeout, a size cap, no redirects and no private addresses, and cached for an hour. **Dynamic client registration** (`/oauth/register`) is the fallback; it only accepts redirect URIs on `OAUTH_REDIRECT_HOSTS`.
3. `/oauth/authorize` validates client, exact redirect URI, `response_type=code`, PKCE `S256` and `resource`, then shows the consent page (pt-BR, no framing, strict CSP). Rudolfo pastes the admin key and picks the scopes; the server creates a **grant** bound to the account and redirects with `code`, `state` and `iss` (RFC 9207).
4. `/oauth/token` exchanges the code (single use, one minute, PKCE verified; a replayed code revokes the grant) for an access token (`lat_…`, one hour) and a refresh token (`lrt_…`, 30 days). Refresh tokens rotate; reusing an old one revokes the whole grant.
5. Access tokens only work on `/mcp` (their audience); `/v1` keeps accepting API keys only. Grants are listed and revoked with the admin key at `/v1/oauth/grants`; revoking the admin key that authorized a grant revokes the grant too.

Only hashes of codes, tokens and client secrets are stored; none of them reach the logs. Every request made with a grant is audited like an API key request.

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
| `get_instructions` | any | Returns a procedure (`conversation`, `classification`, `notifications`) in Markdown |
| `list_categories` | `expenses:read` | Lists the account's category codes and labels |
| `save_category` | `expenses:write` | Creates a category or renames an existing code |
| `record_expense` | `expenses:write` | Records an expense Case understood from the conversation |
| `update_transaction` | `expenses:write` | Corrects description, merchant, payee, amount, date, payment method, nature, purpose or installments, with a reason |
| `set_category` | `expenses:write` | Sets the transaction's category (confirmed by Rudolfo, or suggested by Case) |
| `void_transaction` | `expenses:write` | Marks a record made by mistake as voided; nothing is deleted |
| `get_transaction` | `expenses:read` | Transaction with categories, evidence, full history and open questions |
| `list_transactions` | `expenses:read` | Lists by purchase period, status, category and payment method, without totals |
| `list_clarifications` | `expenses:read` | Lists pending questions by kind, status and purchase period |
| `resolve_clarification` | `expenses:write` | Answers (`resolve`) or closes (`dismiss`) a question |
| `list_pending_notifications` | `expenses:read` | Google Wallet notifications waiting for Case's interpretation, oldest first, with earlier versions |
| `submit_notification_interpretations` | `expenses:write` | Applies Case's reading of up to 50 notification versions, each on its own |
| `get_coverage_status` | `expenses:read` | Tracking state, gaps, pauses, late deliveries, inbox and phone status for a period of days, with the limitations to relay |
| `set_tracking` | `expenses:write` | Turns automatic tracking on or off and sets the inbox interval, only when Rudolfo asks |

Every write tool takes a `request_id` that Case generates once per intention and reuses on retries; the ledger runs it through `idempotency.Do` in the same transaction. `submit_notification_interpretations` is the exception: it is idempotent per notification version (see [Interpreting notifications](#interpreting-notifications)).

## Sensitive data

Every free-text value that reaches the database (conversation text, notification text, statement excerpts sent by Case) goes through `redaction.Text` or `redaction.Values` before it is persisted, and every log line goes through `redaction.NewHandler`:

- card numbers (13 to 19 digits, optionally grouped by spaces or hyphens, with a valid Luhn check digit) keep only the last four digits;
- valid CPFs are masked completely;
- authentication codes, OTPs, CVVs, passwords and tokens (labelled values such as `código: 482913`, `Bearer …`, `lst_…` keys and JWT-like strings) are replaced by `[redacted]`.

The redaction returns what it found as `{kind, count}` pairs, which are stored next to the evidence for audit; the redacted values themselves are never kept. Only Google Wallet notifications (`redaction.AllowedNotificationPackage`) are accepted from the Android app.

## Lint conventions

`.golangci.yml` follows the repository conventions: code and comments in English (`misspell` with the US locale) and no rule that demands doc comments (`staticcheck` ST1000 and ST1020–ST1022 are disabled, and `revive` is not enabled).
