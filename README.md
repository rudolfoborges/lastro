# lastro

Personal expense tracking reached through Case, the user's AI assistant, over MCP. Case understands and classifies; this repository's service stores, validates and computes. See `docs/prd.md` for the product requirements and `CLAUDE.md` for the architecture principle and code conventions.

## Layout

| Path | Contents |
| --- | --- |
| `apps/api` | Go service (`lastro` binary): HTTP API, migrations and CLI. See [`apps/api/README.md`](apps/api/README.md) |
| `docs` | Product requirements |
| `compose.yaml` | Local environment |

Tasks run through Nx: `pnpm install`, then `pnpm nx run api:<target>`.

## Local environment

Requirements: Docker with Compose v2.

```bash
docker compose up            # Postgres, migrations and the API with hot reload on :8080
curl localhost:8080/readyz   # {"status":"ready"}
```

| Service | What it does |
| --- | --- |
| `postgres` | Postgres 17 on `localhost:5432` (`postgres`/`postgres`), with the `lastro` database and the empty `lastro_test` database used by integration tests |
| `migrate` | One-shot `lastro migrate up` against the current source |
| `api` | The API rebuilt by [air](https://github.com/air-verse/air) whenever a `.go` or `.sql` file changes |
| `pgweb` | Web SQL client on `localhost:8081`, only with `docker compose --profile tools up` |

Create a development account with synthetic keys (`admin`, `case` with expense and statement scopes, `android` with `wallet:ingest`):

```bash
docker compose exec api go run ./cmd/lastro seed
```

Run the integration tests against the compose database:

```bash
cp apps/api/.env.example apps/api/.env   # sets TEST_DATABASE_URL to lastro_test
pnpm nx run api:test
```

`docker compose down -v` removes the containers and every volume. Ports can be changed with `POSTGRES_PORT`, `API_PORT` and `PGWEB_PORT`. Only synthetic data belongs in this environment: never load real statements, notifications or amounts into a repository that is public.
