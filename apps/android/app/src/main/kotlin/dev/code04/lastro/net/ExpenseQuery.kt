package dev.code04.lastro.net

import java.net.URLEncoder

data class ExpenseQuery(
    val month: String? = null,
    val from: String? = null,
    val to: String? = null,
    val currency: String = "BRL",
    val statuses: List<String> = emptyList(),
    val natures: List<String> = emptyList(),
    val categories: List<String> = emptyList(),
    val classifications: List<String> = emptyList(),
    val text: String = "",
    val label: String? = null,
    val origin: String? = null,
) {
    val filtered: Boolean
        get() = statuses.isNotEmpty() || natures.isNotEmpty() || categories.isNotEmpty() || classifications.isNotEmpty() || text.isNotBlank()

    val activeFilters: Int
        get() = listOf(categories.isNotEmpty() || label != null, classifications.isNotEmpty(), statuses.isNotEmpty(), text.isNotBlank()).count { it }

    fun cleared(): ExpenseQuery = ExpenseQuery(month = month ?: from?.take(7), currency = currency)

    fun path(offset: Int = 0, limit: Int = PAGE): String {
        val params = buildList {
            if (from != null && to != null) {
                add("from" to from)
                add("to" to to)
            } else if (month != null) {
                add("month" to month)
            }
            add("currency" to currency)
            statuses.forEach { add("status" to it) }
            natures.forEach { add("nature" to it) }
            categories.forEach { add("category" to it) }
            classifications.forEach { add("classification" to it) }
            if (text.isNotBlank()) add("q" to text.trim())
            add("limit" to limit.toString())
            if (offset > 0) add("offset" to offset.toString())
        }
        return "/v1/expenses?" + params.joinToString("&") { (key, value) -> "$key=${encode(value)}" }
    }

    companion object {
        const val PAGE = 50

        fun ofGroup(group: CategoryGroup, origin: String): ExpenseQuery {
            val filter = group.filter
            return ExpenseQuery(
                from = filter["from"]?.firstOrNull(),
                to = filter["to"]?.firstOrNull(),
                currency = filter["currency"]?.firstOrNull() ?: "BRL",
                statuses = filter["status"].orEmpty(),
                natures = filter["nature"].orEmpty(),
                categories = filter["category"].orEmpty(),
                classifications = filter["classification"].orEmpty(),
                label = group.label,
                origin = origin,
            )
        }

        private fun encode(value: String): String = URLEncoder.encode(value, Charsets.UTF_8)
    }
}
