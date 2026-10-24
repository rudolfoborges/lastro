package dev.code04.lastro

import dev.code04.lastro.net.CaseReading
import dev.code04.lastro.net.DeliveryReceipt
import dev.code04.lastro.net.ServerNotifications
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.time.Instant

class ServerNotificationsTest {
    @Test
    fun readsTheCaseReadingAndTheTransactionOfEachVersion() {
        val body = JSONObject(
            """
            {"notifications": [
              {"notification_id": "n1", "notification_key": "k1", "version": 2, "event": "updated",
               "posted_at": "2026-10-22T15:04:05Z", "received_at": "2026-10-22T15:04:25Z", "status": "interpreted",
               "interpretation": {"event_type": "purchase_update", "effect": "updated",
                 "transaction": {"status": "provisional", "category": {"code": "meals", "label": "Alimentação e refeições", "status": "suggested"}}}},
              {"notification_id": "n2", "notification_key": "k2", "version": 1, "event": "posted",
               "posted_at": "2026-10-22T16:00:00Z", "received_at": "2026-10-22T16:00:10Z", "status": "pending_interpretation"}
            ]}
            """,
        )

        val notifications = ServerNotifications.parse(body)

        assertEquals(2, notifications.size)
        assertEquals(Instant.parse("2026-10-22T15:04:05Z"), notifications[0].postedAt)
        assertEquals(CaseReading("purchase_update", "updated", "provisional", "Alimentação e refeições"), notifications[0].reading)
        assertNull(notifications[1].reading)
    }

    @Test
    fun anUnexpectedBodyMeansNoNotifications() {
        assertEquals(emptyList<Any>(), ServerNotifications.parse(null))
        assertEquals(emptyList<Any>(), ServerNotifications.parse(JSONObject("""{"error": "forbidden"}""")))
    }

    @Test
    fun theDeviceIdIsEncodedInThePath() {
        assertEquals(
            "/v1/wallet/notifications?device_id=android-1a2b%3A3&limit=50",
            ServerNotifications.path("android-1a2b:3"),
        )
    }

    @Test
    fun aReceiptKeepsTheNotificationAndVersion() {
        assertEquals(
            DeliveryReceipt("stored", "n1", 2),
            DeliveryReceipt.from(JSONObject("""{"result": "stored", "notification_id": "n1", "version": 2}""")),
        )
        assertEquals(DeliveryReceipt("tracking_paused", null, null), DeliveryReceipt.from(JSONObject("""{"result": "tracking_paused"}""")))
    }
}
