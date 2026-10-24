# Operations

Production runs on [Railway](https://railway.com): one `api` service built from `apps/api/Dockerfile` and one Postgres database, both in the `production` environment of the `lastro` project. The infrastructure is declared in [`.railway/railway.ts`](../.railway/railway.ts) (Railway Infrastructure as Code); `railway.json` is deprecated and must not be added.

## What the IaC declares

| Resource | Settings |
| --- | --- |
| `postgres` | Railway Postgres (`postgres-ssl:18`), reached by the API over the private network |
| `api` | Source: GitHub `rudolfoborges/lastro`, root directory `/apps/api`, built from its `Dockerfile` |
| | Pre-deploy: `lastro migrate up`, so migrations run before the new version receives traffic and a failure aborts the deploy |
| | Healthcheck `GET /readyz` (60s), restart on failure (5 retries) |
| | Variables: `DATABASE_URL` (reference to the database's private URL), `LASTRO_ENV=production`, `LOG_LEVEL=info`, `TRUST_PROXY_HEADERS=true` (Railway's edge sets `X-Real-IP`) |

Secrets live only in Railway variables. Never commit them to `.railway/railway.ts`.

## First-time setup

1. Sign in and create or link the project from the repository root:

   ```bash
   railway login
   railway init --name lastro      # or: railway link --project lastro --environment production
   ```

2. Review and apply the infrastructure:

   ```bash
   pnpm install
   railway config plan
   railway config apply            # only after reviewing the plan
   ```

3. Wire the deploy job: in the project's **Settings → Tokens**, create a token for `production` and store it as the repository secret `RAILWAY_TOKEN` (`gh secret set RAILWAY_TOKEN`). Store the service and environment IDs as the repository variables `RAILWAY_SERVICE_ID` and `RAILWAY_ENVIRONMENT_ID` (`gh variable set`).

4. Custom domain: Railway configuration cannot register custom domains, so create it with the CLI and then list it in `domains` of the `api` service in `.railway/railway.ts` (`railway config plan` must report no changes):

   ```bash
   railway domain <host> --service api          # may print "Failed to create custom domain" even when it succeeds
   railway domain status <host> --service api   # CNAME target and the _railway-verify TXT record
   ```

   Create both DNS records at the zone's provider, **DNS only** (no Cloudflare proxy: it would terminate TLS, see the traffic in clear and hide the client IP from the rate limit). The certificate is issued once Railway sees them. Production is served at the Railway domain (`https://api-production-9ee63.up.railway.app`), which is also `PUBLIC_BASE_URL`; Case and the Android app use it for `/mcp` and the wallet webhook. `lastro.code04.dev` stays registered but parked: its certificate hit the Let's Encrypt rate limit (#83). Moving to a custom domain requires changing `PUBLIC_BASE_URL` and re-authorizing ChatGPT, because OAuth tokens are bound to the address they were issued for.
5. Keep Postgres private: if the database service has a TCP proxy (public networking), remove it. The API only needs the private network.
6. Create the first account:

   ```bash
   railway ssh keys add            # once, registers a local public key
   railway ssh --service api lastro invites create --note "Rudolfo"
   ```

   Then sign up with the code through `POST /v1/accounts` (see `apps/api/README.md`) and keep the returned `admin` token in a password manager.

## Lost admin key

Tokens are stored only as hashes, so a lost key cannot be recovered; the operator issues a new one. Run the commands in your own terminal, so the token is never pasted anywhere else:

```bash
railway ssh --service api lastro accounts list
railway ssh --service api lastro keys create --account <account-id> --name admin --scopes admin
```

Keep the new token in the password manager. Then list the keys with it (`GET /v1/api-keys`) and revoke the lost one (`POST /v1/api-keys/{id}/revoke`). Case's OAuth authorization in ChatGPT does not depend on the admin key and keeps working.

## Deploys

Deploys are triggered by the `deploy` job of the CI workflow, not by a Railway GitHub trigger. On a push to `main` that changes `apps/api`, the job waits for the `ci` job to pass and asks Railway, with `RAILWAY_TOKEN`, to deploy that exact commit (`.github/scripts/railway-deploy.sh`). Railway builds the image, runs `lastro migrate up` in a pre-deploy container, waits for `/readyz` and only then moves traffic; the job fails when the deployment fails.

Do not also connect the service to a Railway GitHub trigger: every push would then deploy twice, and once before CI finished. To deploy `main` by hand, run the CI workflow from the Actions tab (`workflow_dispatch`), which always deploys, or `railway redeploy --service api --from-source`.

To roll back, redeploy the previous deployment from the Railway dashboard (images are kept for 72 hours on Hobby). Migrations are not rolled back automatically: when a release must be reverted together with its schema change, run `lastro migrate down` through `railway ssh` before redeploying.

## Backups and restore

**There are no backups today.** The workspace is on the Hobby plan, where Railway rejects manual backups and backup schedules ("only available for Pro workspaces"), and while the project is experimental it runs without them: losing the Postgres volume loses the data.

Before the new control becomes the main record of expenses (issue #50), pick one of these:

- Move the workspace to Pro and enable the daily, weekly and monthly schedules on the Postgres service's **Backups** tab (retained 6, 27 and 89 days). The restore procedure below applies.
- Add a scheduled, encrypted `pg_dump` to a bucket.

The independent export of the records (PRD §11) is covered by issue #47.

### Restoring a native backup (Pro)

Backups can only be restored into the same project and environment, and wiping a volume deletes its backups.

1. On the Postgres service, open **Backups**, choose the backup and select **Restore**.
2. Railway stages a new volume, named after the backup date, mounted at the same path; the current volume is kept but unmounted.
3. Review the staged change and select **Deploy**.
4. Verify: `curl https://<host>/readyz`, `lastro migrate status` through `railway ssh --service api`, and that `GET /v1/whoami` still works with an existing key.

To rehearse the procedure without touching production, create a separate throwaway project from the same IaC, run `lastro seed` there, take a manual backup, change some data, restore and verify. Delete the project afterwards.

## Credentials

- API keys: create and revoke them with an `admin` key through `/v1/api-keys`. Revocation is effective on the next request.
- Invites: single use and expiring; create them only when someone is about to sign up.
