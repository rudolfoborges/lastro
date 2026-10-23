package dev.code04.lastro.net

import dev.code04.lastro.capture.CapturedNotification
import dev.code04.lastro.capture.NotificationEvent
import org.json.JSONObject
import java.time.Instant

object Payloads {
    fun notification(deviceId: String, captured: CapturedNotification): JSONObject = JSONObject().apply {
        put("device_id", deviceId)
        put("package", captured.packageName)
        put("notification_key", captured.key)
        put("event", captured.event.wire)
        if (captured.event != NotificationEvent.REMOVED) {
            put("posted_at", Instant.ofEpochMilli(captured.postTime).toString())
            put("title", captured.title)
            put("text", captured.text)
            put("big_text", captured.bigText)
        }
    }

    fun heartbeat(
        deviceId: String,
        clientVersion: String,
        notificationAccess: Boolean,
        batteryUnrestricted: Boolean,
        queueSize: Int,
    ): JSONObject = JSONObject().apply {
        put("device_id", deviceId)
        put("client_version", clientVersion)
        put("notification_access", notificationAccess)
        put("battery_unrestricted", batteryUnrestricted)
        put("queue_size", queueSize)
    }

    fun stamped(body: JSONObject, sentAt: Instant): JSONObject = JSONObject(body.toString()).put("sent_at", sentAt.toString())
}
