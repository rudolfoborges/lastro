package dev.code04.lastro.capture

import java.security.MessageDigest

const val GOOGLE_WALLET = "com.google.android.apps.walletnfcrel"

enum class NotificationEvent(val wire: String) {
    POSTED("posted"),
    UPDATED("updated"),
    REMOVED("removed"),
}

data class RawNotification(
    val packageName: String,
    val key: String,
    val postTime: Long,
    val title: String?,
    val text: String?,
    val bigText: String?,
)

data class CapturedNotification(
    val packageName: String,
    val key: String,
    val event: NotificationEvent,
    val postTime: Long,
    val title: String,
    val text: String,
    val bigText: String,
    val contentHash: String,
)

object CapturePolicy {
    fun isAllowed(packageName: String, allowlist: Set<String>): Boolean = packageName in allowlist

    fun capture(raw: RawNotification, allowlist: Set<String>, previousHash: String?): CapturedNotification? {
        if (!isAllowed(raw.packageName, allowlist)) return null
        val title = Redaction.redact(raw.title.orEmpty().trim())
        val text = Redaction.redact(raw.text.orEmpty().trim())
        val bigText = Redaction.redact(raw.bigText.orEmpty().trim())
        if (title.isEmpty() && text.isEmpty() && bigText.isEmpty()) return null
        val hash = sha256(listOf(raw.packageName, title, text, bigText, raw.postTime.toString()).joinToString("\u001f"))
        if (hash == previousHash) return null
        val event = if (previousHash == null) NotificationEvent.POSTED else NotificationEvent.UPDATED
        return CapturedNotification(raw.packageName, raw.key, event, raw.postTime, title, text, bigText, hash)
    }

    fun removal(packageName: String, key: String, allowlist: Set<String>, previousHash: String?): CapturedNotification? {
        if (!isAllowed(packageName, allowlist) || previousHash == null) return null
        return CapturedNotification(packageName, key, NotificationEvent.REMOVED, 0, "", "", "", previousHash)
    }

    private fun sha256(value: String): String =
        MessageDigest.getInstance("SHA-256").digest(value.toByteArray()).joinToString("") { "%02x".format(it) }
}
