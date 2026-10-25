# Security and privacy review of the first delivery

Review of #48, done on 2026-10-24 against `main` after M4 and the M5 query, rule and export work. It covers PRD §2.7 and §11 and criterion 14.

## Checklist

| Item | Result | Evidence |
| --- | --- | --- |
| Every endpoint and tool requires authentication and a scope; no open page | **Holds** | `httpapi/security_test.go`: every route with data answers 401 without a key and 403 with a key of another scope. `mcpserver/security_test.go`: every MCP tool but `get_instructions` refuses a credential without its scope. Public routes are `/healthz`, `/readyz`, the invite-only sign-up, the OAuth flow, and the export download, which takes a 256-bit secret valid for 15 minutes (#47) |
| Logs, errors and responses carry no PAN, OTP, tokens or unneeded data | **Holds** | `redaction.Text` runs on all free text before it is stored, and `redaction.NewHandler` on every log line. Criterion 14 test: a card number and a code sent in a message never reach the answer, the stored transaction or the logs, while the last four digits are kept. Export tokens are logged by route pattern only. Tool calls are audited without arguments |
| Rotation of tokens and secrets is documented | **Holds** | `docs/operations.md`, *Credentials*. There is no HMAC secret: the phone authenticates with a revocable API key (M0 decision) |
| TLS on every connection; Postgres only on Railway's private network | **Holds, one check left to Rudolfo** | Railway terminates TLS for the public domain, and the app and the phone only use `https://`. `DATABASE_URL` is the private URL of the Postgres service. **Check** that the Postgres service has no public TCP proxy (*Settings → Networking*); remove it if it is there |
| `govulncheck` clean; minimal, non-root image | **Holds** | CI runs `vulncheck` on every push. The runtime image is Alpine with only `ca-certificates` and `tzdata`, running as the unprivileged user `lastro` (uid 10001) |
| Android app: no third-party SDK, credentials in the Keystore, nothing outside the allowlist leaves the phone | **Holds** | Dependencies are AndroidX, Compose, WorkManager and `org.json` only. The API key is encrypted with AES-GCM under a key in the Android Keystore (`SecretBox`). `CapturePolicy` keeps only Google Wallet, and the server discards any other package |
| Railway access reviewed | **Left to Rudolfo** | Only the project members can be reviewed, in the Railway dashboard (*Project → Members*). Keep it to Rudolfo |
| The repository is public: no real data was committed, including in history | **Holds** | A scan of the whole history (`git log --all -p`) found: no mention of the bank; only well-known test card numbers and an example CPF in the redaction tests; no figure from the real statement; and only Rudolfo's e-mail as commit author |

## Criterion 14

"Consultas e resumos não expõem informações a pessoas não autorizadas nem incluem dados secretos desnecessários":

- **Isolation:** another account sees nothing. `get_transaction` answers `not_found`, and the listings and totals are empty. Row-level security keys every tenant table by the account, and the tests run as the restricted role.
- **No secrets in answers:** the criterion 14 test (`mcpserver/security_test.go`) checks the answer, the stored transaction and the logs.

## Follow-ups for Rudolfo

1. Postgres networking: confirm that there is no public TCP proxy.
2. Railway members: confirm that only Rudolfo has access.
3. Keep exporting regularly (`export_data`, "exporte meus dados"). There is no automatic backup (#47).
