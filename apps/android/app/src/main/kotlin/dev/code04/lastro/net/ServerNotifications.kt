package dev.code04.lastro.net

import org.json.JSONObject
import java.net.URLEncoder
import java.time.Instant
import java.time.format.DateTimeParseException

data class CaseReading(
    val eventType: String,
    val effect: String,
    val transactionStatus: String?,
    val category: String?,
    val transactionId: String? = null,
)

data class ServerNotification(
    val notificationId: String,
    val notificationKey: String,
    val version: Int,
    val postedAt: Instant?,
    val reading: CaseReading?,
)

object ServerNotifications {
    const val LIMIT = 100

    fun path(deviceId: String, limit: Int = LIMIT): String =
        "/v1/wallet/notifications?device_id=${URLEncoder.encode(deviceId, Charsets.UTF_8)}&limit=$limit"

    fun parse(body: JSONObject?): List<ServerNotification> {
        val items = body?.optJSONArray("notifications") ?: return emptyList()
        return (0 until items.length()).mapNotNull { i -> items.optJSONObject(i)?.let(::notification) }
    }

    private fun notification(item: JSONObject): ServerNotification? {
        val id = item.optString("notification_id").takeIf { it.isNotEmpty() } ?: return null
        return ServerNotification(
            notificationId = id,
            notificationKey = item.optString("notification_key"),
            version = item.optInt("version"),
            postedAt = instant(item.optString("posted_at")),
            reading = item.optJSONObject("interpretation")?.let(::reading),
        )
    }

    private fun reading(interpretation: JSONObject): CaseReading {
        val transaction = interpretation.optJSONObject("transaction")
        return CaseReading(
            eventType = interpretation.optString("event_type"),
            effect = interpretation.optString("effect"),
            transactionStatus = transaction?.optString("status")?.takeIf { it.isNotEmpty() },
            category = transaction?.optJSONObject("category")?.optString("label")?.takeIf { it.isNotEmpty() },
            transactionId = interpretation.optString("transaction_id").takeIf { it.isNotEmpty() },
        )
    }

    private fun instant(value: String): Instant? = try {
        if (value.isEmpty()) null else Instant.parse(value)
    } catch (_: DateTimeParseException) {
        null
    }
}
