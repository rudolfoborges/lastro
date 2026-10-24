package dev.code04.lastro.net

import org.json.JSONObject

data class DeliveryReceipt(val result: String, val notificationId: String?, val version: Int?) {
    companion object {
        fun from(body: JSONObject?): DeliveryReceipt = DeliveryReceipt(
            result = body?.optString("result").orEmpty(),
            notificationId = body?.optString("notification_id")?.takeIf { it.isNotEmpty() },
            version = body?.optInt("version", 0)?.takeIf { it > 0 },
        )
    }
}
