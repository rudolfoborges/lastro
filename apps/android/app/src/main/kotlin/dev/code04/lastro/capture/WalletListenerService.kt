package dev.code04.lastro.capture

import android.app.Notification
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification
import dev.code04.lastro.data.LocalStore
import dev.code04.lastro.data.Settings
import dev.code04.lastro.net.Payloads
import dev.code04.lastro.work.Scheduling

class WalletListenerService : NotificationListenerService() {
    override fun onListenerConnected() {
        activeNotifications?.forEach(::onNotificationPosted)
    }

    override fun onNotificationPosted(sbn: StatusBarNotification) {
        val settings = Settings(this)
        if (settings.paused) return
        val store = LocalStore.get(this)
        val extras = sbn.notification.extras
        val raw = RawNotification(
            packageName = sbn.packageName,
            key = sbn.key,
            postTime = sbn.postTime,
            title = extras.getCharSequence(Notification.EXTRA_TITLE)?.toString(),
            text = extras.getCharSequence(Notification.EXTRA_TEXT)?.toString(),
            bigText = extras.getCharSequence(Notification.EXTRA_BIG_TEXT)?.toString(),
        )
        val captured = CapturePolicy.capture(raw, settings.allowlist, store.seenHash(sbn.key)) ?: return
        keep(settings, store, captured)
    }

    override fun onNotificationRemoved(sbn: StatusBarNotification) {
        val settings = Settings(this)
        if (settings.paused) return
        val store = LocalStore.get(this)
        val captured = CapturePolicy.removal(sbn.packageName, sbn.key, settings.allowlist, store.seenHash(sbn.key)) ?: return
        keep(settings, store, captured)
    }

    private fun keep(settings: Settings, store: LocalStore, captured: CapturedNotification) {
        val outbox = if (settings.readyToSend) Payloads.notification(settings.deviceId, captured).toString() else null
        store.record(captured, outbox)
        if (outbox != null) Scheduling.deliverSoon(this)
    }
}
