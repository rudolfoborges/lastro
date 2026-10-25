# android

Rudolfo's own Android app. It captures Google Wallet notifications and sends them to Lastro, and it shows, read-only, what is registered: the month's expenses, where they concentrate, how they compare with the month before and whether the collection is up to date. It keeps the data under his control: only the apps on the list are read, the text is redacted on the phone, and nothing leaves the phone until a destination is configured and sending is turned on.

The app never creates, changes, confirms or reconciles an expense. Recording, correcting and classifying stay in the conversation with Case; the app only navigates, filters and inspects.

Kotlin, Jetpack Compose and WorkManager. No third-party SDK (no analytics, crash reporting or push), and the only permissions are internet access and the request to ignore battery optimization. The notification listener permission is granted by Rudolfo in the system settings.

## Commands

| Command | What it does |
| --- | --- |
| `pnpm nx build android` | Debug APK in `app/build/outputs/apk/debug` |
| `pnpm nx test android` | JVM unit tests (redaction, package filter, payloads, delivery outcomes, API answers, list filters, formatting, collection counts) |
| `pnpm nx lint android` | Android lint |
| `pnpm nx release android` | Release APK, signed when `keystore.properties` exists |

Requirements: JDK 17 or newer and the Android SDK with platform 37 (Android 17). Locally, `local.properties` points at the SDK (`sdk.dir=…`); in CI, `ANDROID_HOME` does.

## How it works

1. `WalletListenerService` (a `NotificationListenerService`) receives each posted, updated and removed notification.
2. Packages outside the list are ignored and never stored or sent. The default list is Google Wallet (`com.google.android.apps.walletnfcrel`), and it can be edited in **Configurações**.
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

The app is always dark, in green. Four destinations at the bottom and **Configurações** at the top, next to the eye that hides values and the refresh button:

- **Visão geral** (`GET /v1/overview`): the month chosen (the current one by default, up to today), when it was updated and the basis (purchase date, BRL). A warning near the total when the coverage is incomplete (pause, phone without a sign of life, notifications Case has not read). The gross total of confirmed expenses with their count and average, the comparison with the same days of the month before (or why it is unavailable), refunds and net, and what stays out of the total (provisional expenses, expenses under review, purchases known only by installment). Bars by category, with the provisional classification and the expenses without category apart from “Outras”; each bar opens its list with the same period and rules. Daily columns where days without expenses, days without coverage (hatched) and days to come look different, with a day picker that reads the value without relying on bar heights. A compact collection status that opens **Coleta**.
- **Despesas** (`GET /v1/expenses`): search, month, category, classification and status filters, the total of what matches under the overview rules, and the list by day with each day's total. Each item shows the expense status (provisional, under review) apart from its category status (provisional, pending), and purchases known only by installment as unavailable. **Limpar filtros** goes back to the month.
- **Despesa** (`GET /v1/expenses/{id}`): the amount and date, the expense status apart from the category status and what that means for the charts, dates and installments, the notifications behind it and whether a statement confirmed it. Corrections happen with Case; there is no edit button.
- **Análises** (`GET /v1/analysis`): gross total by month for six months, the month in progress marked and never ranked against whole months, months the Lastro never covered marked as without data, a table alternative, the same-days comparison and the change by concluded category.
- **Coleta**: the path capture → sending → processing, the notifications captured in the last 7 days counted by their current stage (awaiting sending, awaiting result, processed, blocked), which add up to the captured total; the sent count apart, as a cumulative milestone; what the processed ones became (an expense, the same expense as another notification, no spending, a doubt); the reasons for blocked ones; and the list by day. A processed notification that became an expense opens it.
- **Configurações**: collection on or paused and the Android permissions; the connection (server, credential masked after saving, captured apps, sending) with what the credential allows; privacy (hide values everywhere, including notification text, and protect the app preview in the recents screen); the history kept on the phone.

States: first use (the steps to start, no sample numbers), loading (no zero stands in for a total), no records (never read as no spending when the coverage is unknown), filter without results, no connection (the last view saved, with its time), query error (retrying only repeats the query), paused collection, partial data and incomplete records.

Opening the app or tapping refresh also asks `GET /v1/wallet/notifications?device_id=…` what Case did with the latest versions from this phone. The answer carries no notification text; the app matches it to its own captures by `notification_id` and `version`, or, for captures made before this history existed, by notification key and post time.

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
2. In **Configurações**, tap **Exportar amostras** and share the JSON with yourself. It holds the redacted text of the notifications from the listed apps only.
3. Before anything goes into the repository (it is public), replace merchants, amounts, card digits and times with synthetic values. The anonymized samples become the fixtures of the server tests and of Case's evaluation.
4. **Apagar do celular** clears the local log.

## Configure sending

1. Create a key with the `wallet:ingest` and `expenses:read` scopes, using the account's `admin` key (a key with `wallet:ingest` only still sends notifications, but the expense screens answer that the credential cannot read):

   ```bash
   curl -X POST https://<host>/v1/api-keys \
     -H "Authorization: Bearer $ADMIN_KEY" -H "Content-Type: application/json" \
     -d '{"name": "android", "scopes": ["wallet:ingest", "expenses:read"]}'
   ```

2. In **Configurações › Conexão**, fill **Servidor** (`https://…`, no path) and the credential, then tap **Salvar**. The credential is encrypted with a key that lives in the Android Keystore, and the card shows what it allows.
3. Turn on **Enviar notificações ao Lastro**. Then ask Case to turn on automatic tracking ("ativa o acompanhamento do Wallet"); **Coleta** shows the path as active after the next heartbeat.

Changing the URL sends everything from then on, including what is still in the queue, only to the new destination.

To try the app in the emulator against a local API, run `adb reverse tcp:8080 tcp:8080` and use `http://127.0.0.1:8080` (debug builds accept `http://`). Android 17 blocks apps targeting it from reaching private addresses such as `10.0.2.2` without the local network permission, so the emulator's host alias times out.

## Pause and revoke

- Turning off **Coletar notificações** stops reading notifications on the phone; the queue and the history are kept, and the paused interval is not recovered later.
- Asking Case to pause tracking makes the server drop what arrives; recorded data stays available.
- To revoke the phone, revoke its key with `POST /v1/api-keys/{id}/revoke` (admin key). The app stops at the next `401` and shows *Credencial recusada*. Tap **Remover credencial** to erase it from the phone.

## Manual checks

- [ ] A real Wallet purchase reaches the server as a pending notification within a few minutes (`list_pending_notifications`).
- [ ] With airplane mode on, a purchase stays **Aguardando envio**; when the network returns, it is delivered once, and **Visão geral** shows the last saved view with its time meanwhile.
- [ ] A notification from another app (WhatsApp, for example) does not appear in the exported samples nor on the server.
- [ ] After Case processes the inbox, **Coleta** shows the notification as processed, opening the expense it became.
- [ ] The total of a category bar in **Visão geral** matches the list it opens.
- [ ] With **Ocultar valores** on, no amount or notification text shows anywhere, and the recents screen does not show the app's content while **Proteger a prévia do app** is on.
- [ ] After changing the URL, new notifications reach only the new server.
