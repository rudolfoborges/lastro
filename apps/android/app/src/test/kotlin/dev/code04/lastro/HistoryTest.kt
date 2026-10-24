package dev.code04.lastro

import dev.code04.lastro.data.CaptureRecord
import dev.code04.lastro.data.Delivery
import dev.code04.lastro.history.History
import dev.code04.lastro.history.ItemStatus
import dev.code04.lastro.history.Labels
import dev.code04.lastro.net.CaseReading
import dev.code04.lastro.net.ServerNotification
import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant

class HistoryTest {
    private val postedAt = Instant.parse("2026-10-22T15:04:05Z")

    private fun capture(id: Long, delivery: Delivery, notificationId: String? = null, version: Int? = null, key: String = "k$id") =
        CaptureRecord(
            id = id,
            notificationKey = key,
            event = "posted",
            postedAt = postedAt.toEpochMilli(),
            title = "Padaria Pão Quente",
            text = "R$ 32,97",
            bigText = "",
            capturedAt = postedAt.toEpochMilli(),
            delivery = delivery,
            deliveryDetail = if (delivery == Delivery.REFUSED) "Notificação recusada (400)" else "",
            notificationId = notificationId,
            version = version,
        )

    private val purchase = CaseReading("purchase", "created", "provisional", "Alimentação e refeições")

    @Test
    fun eachCaptureShowsWhereItStands() {
        val captures = listOf(
            capture(1, Delivery.STORED, "n1", 1),
            capture(2, Delivery.STORED, "n2", 1),
            capture(3, Delivery.DUPLICATE, "n3", 2),
            capture(4, Delivery.QUEUED),
            capture(5, Delivery.LOCAL_ONLY),
            capture(6, Delivery.REFUSED),
            capture(7, Delivery.TRACKING_PAUSED),
        )
        val server = listOf(
            ServerNotification("n1", "k1", 1, postedAt, purchase),
            ServerNotification("n2", "k2", 1, postedAt, null),
            ServerNotification("n3", "k3", 1, postedAt, purchase),
        )

        val statuses = History.merge(captures, server).map { it.status }

        assertEquals(
            listOf(
                ItemStatus.Processed(purchase),
                ItemStatus.AwaitingCase,
                ItemStatus.Sent,
                ItemStatus.Queued,
                ItemStatus.OnlyOnPhone,
                ItemStatus.Refused("Notificação recusada (400)"),
                ItemStatus.TrackingPaused,
            ),
            statuses,
        )
    }

    @Test
    fun capturesFromBeforeTheHistoryAreMatchedByKeyAndPostTime() {
        val captures = listOf(capture(1, Delivery.UNKNOWN, key = "wallet-1"), capture(2, Delivery.UNKNOWN, key = "wallet-2"))
        val server = listOf(
            ServerNotification("n1", "wallet-1", 1, postedAt, null),
            ServerNotification("n1", "wallet-1", 2, postedAt, purchase),
        )

        val statuses = History.merge(captures, server).map { it.status }

        assertEquals(listOf(ItemStatus.Processed(purchase), ItemStatus.Unknown), statuses)
    }

    @Test
    fun withoutTheServerSentCapturesStaySent() {
        val statuses = History.merge(listOf(capture(1, Delivery.STORED, "n1", 1)), null).map { it.status }

        assertEquals(listOf(ItemStatus.Sent), statuses)
    }

    @Test
    fun theSummaryCountsWhatCaseStillHasToRead() {
        val items = History.merge(
            listOf(capture(1, Delivery.STORED, "n1", 1), capture(2, Delivery.STORED, "n2", 1), capture(3, Delivery.QUEUED), capture(4, Delivery.REFUSED)),
            listOf(ServerNotification("n1", "k1", 1, postedAt, purchase), ServerNotification("n2", "k2", 1, postedAt, null)),
        )

        val summary = History.summarize(items)

        assertEquals(1, summary.awaitingCase)
        assertEquals(1, summary.processed)
        assertEquals(2, summary.notSent)
    }

    @Test
    fun labelsSayWhatCaseDid() {
        val processed = ItemStatus.Processed(purchase)

        assertEquals("Compra registrada", Labels.status(processed))
        assertEquals("Alimentação e refeições · provisória", Labels.details(processed))
        assertEquals("Dúvida aberta", Labels.status(ItemStatus.Processed(CaseReading("unknown", "none", null, null))))
        assertEquals("Pagamento recusado", Labels.status(ItemStatus.Processed(CaseReading("declined", "none", null, null))))
        assertEquals("Aguardando a Case", Labels.status(ItemStatus.AwaitingCase))
    }
}
