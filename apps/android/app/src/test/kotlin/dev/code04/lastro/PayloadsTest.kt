package dev.code04.lastro

import dev.code04.lastro.capture.CapturedNotification
import dev.code04.lastro.capture.NotificationEvent
import dev.code04.lastro.net.DeliveryOutcome
import dev.code04.lastro.net.Payloads
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test
import java.time.Instant

class PayloadsTest {
    private val captured = CapturedNotification(
        packageName = "com.google.android.apps.walletnfcrel",
        key = "0|wallet|7|null|10123",
        event = NotificationEvent.POSTED,
        postTime = Instant.parse("2026-10-22T15:04:05Z").toEpochMilli(),
        title = "Padaria Pão Quente",
        text = "R$ 32,97 com Visa •••• 1234",
        bigText = "",
        contentHash = "abc",
    )

    @Test
    fun aNotificationFollowsTheServerContractWithoutSentAt() {
        val body = Payloads.notification("galaxy-s24", captured)

        assertEquals(
            setOf("device_id", "package", "notification_key", "event", "posted_at", "title", "text", "big_text"),
            body.keys().asSequence().toSet(),
        )
        assertEquals("2026-10-22T15:04:05Z", body.getString("posted_at"))
        assertEquals("posted", body.getString("event"))
    }

    @Test
    fun eachAttemptGetsAFreshSentAtAndKeepsTheContent() {
        val stored = Payloads.notification("galaxy-s24", captured)
        val first = Payloads.stamped(stored, Instant.parse("2026-10-22T15:05:00Z"))
        val retry = Payloads.stamped(stored, Instant.parse("2026-10-22T18:00:00Z"))

        assertFalse(stored.has("sent_at"))
        assertEquals("2026-10-22T18:00:00Z", retry.getString("sent_at"))
        first.remove("sent_at")
        retry.remove("sent_at")
        assertEquals(first.toString(), retry.toString())
    }

    @Test
    fun aRemovalCarriesOnlyTheIdentity() {
        val body = Payloads.notification("galaxy-s24", captured.copy(event = NotificationEvent.REMOVED))

        assertEquals(setOf("device_id", "package", "notification_key", "event"), body.keys().asSequence().toSet())
    }

    @Test
    fun deliveryOutcomesFollowTheServerAnswer() {
        assertEquals(DeliveryOutcome.DELIVERED, DeliveryOutcome.of(201, null))
        assertEquals(DeliveryOutcome.DELIVERED, DeliveryOutcome.of(200, null))
        assertEquals(DeliveryOutcome.STOP, DeliveryOutcome.of(401, "unauthorized"))
        assertEquals(DeliveryOutcome.RETRY_LATER, DeliveryOutcome.of(429, "rate_limited"))
        assertEquals(DeliveryOutcome.RETRY_LATER, DeliveryOutcome.of(503, null))
        assertEquals(DeliveryOutcome.RETRY_LATER, DeliveryOutcome.of(400, "stale_request"))
        assertEquals(DeliveryOutcome.DROP, DeliveryOutcome.of(400, "invalid_request"))
    }
}
