# android

Rudolfo's own Android app that captures Google Wallet notifications and sends them to Lastro. It keeps the data under his control: only the apps on the list are read, the text is redacted on the phone, and nothing leaves the phone until a destination is configured and sending is turned on.

Kotlin, Jetpack Compose and WorkManager. No third-party SDK (no analytics, crash reporting or push), and the only permissions are internet access and the request to ignore battery optimization. The notification listener permission is granted by Rudolfo in the system settings.

## Commands

| Command | What it does |
| --- | --- |
| `pnpm nx build android` | Debug APK in `app/build/outputs/apk/debug` |
| `pnpm nx test android` | JVM unit tests (redaction, package filter, payloads, delivery outcomes) |
| `pnpm nx lint android` | Android lint |
| `pnpm nx release android` | Release APK, signed when `keystore.properties` exists |

Requirements: JDK 17 or newer and the Android SDK with platform 37 (Android 17). Locally, `local.properties` points at the SDK (`sdk.dir=…`); in CI, `ANDROID_HOME` does.

## How it works

1. `WalletListenerService` (a `NotificationListenerService`) receives each posted, updated and removed notification.
2. Packages outside the list are ignored and never stored or sent. The default list is Google Wallet (`com.google.android.apps.walletnfcrel`), and it can be edited in **Ajustes**.
3. Title, text and big text are redacted on the phone: card numbers keep only the last four digits, and authentication codes become `[redacted]`. The server redacts again.
4. The notification is kept in a local log (the last 500, for the spike), and, when sending is on, in a persistent queue.
5. `DeliveryWorker` sends the queue to `POST /v1/wallet/notifications` when there is network, with exponential backoff:
   - each attempt carries a fresh `sent_at` and the same content, so the server answers a retry as a duplicate and never stores it twice;
   - `401` or `403` stops sending until the key is fixed;
   - `429`, `5xx`, `stale_request` and network failures retry later;
   - other `400` answers drop the item and show the reason.
6. `HeartbeatWorker` calls `POST /v1/wallet/heartbeat` every 15 minutes with the version, notification access, battery restriction and queue size. The answer shows whether tracking is active on the server.
7. Each capture keeps the result of its delivery (`stored`, `duplicate`, `tracking_paused`, `discarded`, refused) and the `notification_id` and `version` the server answered.

The server only stores notifications while automatic tracking is active, which Rudolfo turns on through Case.

## Screens

The app is always dark, in green, with three tabs:

- **Início:** the last notifications counted as awaiting Case, processed and not sent; capture health (notification access, battery, pause, sending); the server state (tracking, queue, last heartbeat and delivery); the last error.
- **Notificações:** the last 50 captures with the redacted text kept on the phone and where each one stands: only on the phone, queued, not stored because tracking was paused, refused, awaiting Case, or what Case did (purchase registered, linked, updated, voided, refund, no effect, open doubt) with the category and whether the purchase is provisional or confirmed.
- **Ajustes:** sending and pause, destination (URL, key, captured apps), Android permissions, and the local history (export samples, clear).

Opening the app or tapping refresh asks `GET /v1/wallet/notifications?device_id=…` (same `wallet:ingest` key) what Case did with the latest versions from this phone. The answer carries no notification text; the app matches it to its own captures by `notification_id` and `version`, or, for captures made before this history existed, by notification key and post time.

## Install

1. Build the APK (`pnpm nx build android` for debug, or `pnpm nx release android` signed with your own key) and install it with `adb install -r app/build/outputs/apk/debug/app-debug.apk`, or copy it to the phone.
2. Open Lastro and tap **Permitir acesso às notificações**. Enable Lastro in the list.
3. Tap **Liberar da economia de bateria**. On the Galaxy (One UI), also check **Settings → Battery → Background usage limits** and make sure Lastro is not under *Sleeping apps* or *Deep sleeping apps*. Otherwise One UI can stop the listener and the heartbeat, and the coverage will show gaps.

### Release signing

The key never goes into the repository. Create one once and describe it in `apps/android/keystore.properties` (ignored by Git):

```bash
keytool -genkeypair -v -keystore lastro-release.jks -alias lastro -keyalg RSA -keysize 4096 -validity 10000
```

```properties
storeFile=lastro-release.jks
storePassword=…
keyAlias=lastro
keyPassword=…
```

Keep the `.jks` and the passwords outside the repository and back them up: an update must be signed with the same key.

## Spike: capture only (#27)

Sending starts **off**. With a destination or without one, the app only keeps what it captured on the phone:

1. Install, grant notification access, and use the Wallet normally for 2 to 3 days (purchases, a declined payment, a refund if possible).
2. In **Ajustes**, tap **Exportar amostras** and share the JSON with yourself. It holds the redacted text of the notifications from the listed apps only.
3. Before anything goes into the repository (it is public), replace merchants, amounts, card digits and times with synthetic values. The anonymized samples become the fixtures of the server tests and of Case's evaluation.
4. **Apagar histórico** clears the local log.

## Configure sending

1. Create a key with the `wallet:ingest` scope only, using the account's `admin` key:

   ```bash
   curl -X POST https://<host>/v1/api-keys \
     -H "Authorization: Bearer $ADMIN_KEY" -H "Content-Type: application/json" \
     -d '{"name": "android", "scopes": ["wallet:ingest"]}'
   ```

2. In **Ajustes**, fill **URL do servidor** (`https://…`, no path) and the key, then tap **Salvar**. The key is encrypted with a key that lives in the Android Keystore.
3. Turn on **Enviar ao servidor**. Then ask Case to turn on automatic tracking ("ativa o acompanhamento do Wallet"); **Início** shows the tracking as `ativo` after the next heartbeat.

Changing the URL sends everything from then on, including what is still in the queue, only to the new destination.

To try the app in the emulator against a local API, run `adb reverse tcp:8080 tcp:8080` and use `http://127.0.0.1:8080` (debug builds accept `http://`). Android 17 blocks apps targeting it from reaching private addresses such as `10.0.2.2` without the local network permission, so the emulator's host alias times out.

## Pause and revoke

- **Pausar captura e envio** stops reading notifications on the phone; the queue is kept.
- Asking Case to pause tracking makes the server drop what arrives; recorded data stays available.
- To revoke the phone, revoke its key with `POST /v1/api-keys/{id}/revoke` (admin key). The app stops at the next `401` and shows *Credencial recusada*. Tap **Remover chave** to erase it from the phone.

## Manual checks

- [ ] A real Wallet purchase reaches the server as a pending notification within a few minutes (`list_pending_notifications`).
- [ ] With airplane mode on, a purchase stays in **Na fila**; when the network returns, it is delivered once.
- [ ] A notification from another app (WhatsApp, for example) does not appear in the exported samples nor on the server.
- [ ] After Case processes the inbox, **Notificações** shows the purchase as registered with its category.
- [ ] After changing the URL, new notifications reach only the new server.
