package dev.code04.lastro.net

enum class DeliveryOutcome {
    DELIVERED,
    RETRY_LATER,
    STOP,
    DROP;

    companion object {
        fun of(status: Int, errorCode: String?): DeliveryOutcome = when {
            status in 200..299 -> DELIVERED
            status == 401 || status == 403 -> STOP
            status == 429 || status >= 500 -> RETRY_LATER
            status == 400 && errorCode == "stale_request" -> RETRY_LATER
            else -> DROP
        }
    }
}
