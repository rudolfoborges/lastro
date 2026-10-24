package dev.code04.lastro.data

enum class Delivery(val wire: String) {
    LOCAL_ONLY("local_only"),
    QUEUED("queued"),
    STORED("stored"),
    DUPLICATE("duplicate"),
    TRACKING_PAUSED("tracking_paused"),
    DISCARDED("discarded"),
    REFUSED("refused"),
    UNKNOWN("unknown");

    val reachedServer: Boolean get() = this == STORED || this == DUPLICATE

    companion object {
        fun of(wire: String?): Delivery = entries.firstOrNull { it.wire == wire } ?: UNKNOWN
    }
}

data class CaptureRecord(
    val id: Long,
    val notificationKey: String,
    val event: String,
    val postedAt: Long,
    val title: String,
    val text: String,
    val bigText: String,
    val capturedAt: Long,
    val delivery: Delivery,
    val deliveryDetail: String,
    val notificationId: String?,
    val version: Int?,
)
