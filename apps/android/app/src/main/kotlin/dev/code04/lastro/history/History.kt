package dev.code04.lastro.history

import dev.code04.lastro.data.CaptureRecord
import dev.code04.lastro.data.Delivery
import dev.code04.lastro.net.CaseReading
import dev.code04.lastro.net.ServerNotification

sealed interface ItemStatus {
    data object OnlyOnPhone : ItemStatus
    data object Queued : ItemStatus
    data object TrackingPaused : ItemStatus
    data object Discarded : ItemStatus
    data class Refused(val reason: String) : ItemStatus
    data object Sent : ItemStatus
    data object AwaitingCase : ItemStatus
    data class Processed(val reading: CaseReading) : ItemStatus
    data object Unknown : ItemStatus
}

data class HistoryItem(val capture: CaptureRecord, val status: ItemStatus)

object History {
    fun merge(captures: List<CaptureRecord>, server: List<ServerNotification>?): List<HistoryItem> {
        val byVersion = server.orEmpty().associateBy { it.notificationId to it.version }
        val byPost = server.orEmpty()
            .groupBy { it.notificationKey to it.postedAt?.toEpochMilli() }
            .mapValues { (_, versions) -> versions.maxBy { it.version } }
        return captures.map { capture ->
            val match = when {
                capture.notificationId != null && capture.version != null -> byVersion[capture.notificationId to capture.version]
                capture.delivery == Delivery.UNKNOWN -> byPost[capture.notificationKey to capture.postedAt]
                else -> null
            }
            HistoryItem(capture, status(capture, match))
        }
    }

    private fun status(capture: CaptureRecord, match: ServerNotification?): ItemStatus = when {
        match != null -> match.reading?.let(ItemStatus::Processed) ?: ItemStatus.AwaitingCase
        else -> when (capture.delivery) {
            Delivery.LOCAL_ONLY -> ItemStatus.OnlyOnPhone
            Delivery.QUEUED -> ItemStatus.Queued
            Delivery.TRACKING_PAUSED -> ItemStatus.TrackingPaused
            Delivery.DISCARDED -> ItemStatus.Discarded
            Delivery.REFUSED -> ItemStatus.Refused(capture.deliveryDetail)
            Delivery.STORED, Delivery.DUPLICATE -> ItemStatus.Sent
            Delivery.UNKNOWN -> ItemStatus.Unknown
        }
    }
}
