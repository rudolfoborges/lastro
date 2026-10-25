package dev.code04.lastro

import dev.code04.lastro.data.CaptureRecord
import dev.code04.lastro.data.Delivery
import dev.code04.lastro.history.CollectionStages
import dev.code04.lastro.history.History
import dev.code04.lastro.history.ItemStatus
import dev.code04.lastro.history.Outcome
import dev.code04.lastro.history.Stage
import dev.code04.lastro.net.CaseReading
import dev.code04.lastro.net.ServerNotification
import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant

class CollectionStagesTest {
    private val postedAt = Instant.parse("2026-10-22T15:04:05Z")

    private fun capture(id: Long, delivery: Delivery, notificationId: String? = null, capturedAt: Instant = postedAt) = CaptureRecord(
        id = id,
        notificationKey = "k$id",
        event = "posted",
        postedAt = capturedAt.toEpochMilli(),
        title = "Padaria",
        text = "R$ 10,00",
        bigText = "",
        capturedAt = capturedAt.toEpochMilli(),
        delivery = delivery,
        deliveryDetail = "",
        notificationId = notificationId,
        version = notificationId?.let { 1 },
    )

    @Test
    fun theCurrentStagesAddUpToTheCapturedOnes() {
        val items = History.merge(
            listOf(
                capture(1, Delivery.STORED, "n1"),
                capture(2, Delivery.STORED, "n2"),
                capture(3, Delivery.STORED, "n3"),
                capture(4, Delivery.QUEUED),
                capture(5, Delivery.REFUSED),
                capture(6, Delivery.LOCAL_ONLY),
                capture(7, Delivery.STORED, "n7", capturedAt = postedAt.minusSeconds(8 * 24 * 3600)),
            ),
            listOf(
                ServerNotification("n1", "k1", 1, postedAt, CaseReading("purchase", "created", "provisional", "Saúde", "t1")),
                ServerNotification("n2", "k2", 1, postedAt, CaseReading("purchase_update", "linked", "provisional", "Saúde", "t1")),
                ServerNotification("n3", "k3", 1, postedAt, null),
            ),
        )

        val summary = CollectionStages.summarize(items, since = postedAt.minusSeconds(7 * 24 * 3600).toEpochMilli())

        assertEquals(6, summary.captured)
        assertEquals(summary.captured, summary.awaitingSend + summary.awaitingResult + summary.processed + summary.blocked + summary.unavailable)
        assertEquals(1, summary.awaitingSend)
        assertEquals(1, summary.awaitingResult)
        assertEquals(2, summary.processed)
        assertEquals(2, summary.blocked)
        assertEquals(3, summary.sent)
        assertEquals(1, summary.becameExpense)
        assertEquals(1, summary.sameExpense)
    }

    @Test
    fun aProcessedNotificationMayNotBecomeAnExpense() {
        assertEquals(Outcome.NO_EXPENSE, CollectionStages.outcome(CaseReading("declined", "none", null, null)))
        assertEquals(Outcome.DOUBT, CollectionStages.outcome(CaseReading("unknown", "none", null, null)))
        assertEquals("sem gasto: pagamento recusado", CollectionStages.explanation(ItemStatus.Processed(CaseReading("declined", "none", null, null))))
        assertEquals(Stage.BLOCKED, CollectionStages.stage(ItemStatus.Refused("formato")))
        assertEquals("envio recusado: formato", CollectionStages.explanation(ItemStatus.Refused("formato")))
    }
}
