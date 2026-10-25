package dev.code04.lastro.net

import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant
import java.time.format.DateTimeParseException

data class Money(val minor: Long, val currency: String, val formatted: String)

data class Tally(val amount: Money, val count: Int)

data class Period(
    val month: String,
    val from: String,
    val to: String,
    val monthEnd: String,
    val inProgress: Boolean,
    val timezone: String,
)

data class Totals(
    val gross: Tally,
    val average: Money?,
    val refunds: Tally,
    val net: Money,
    val provisional: Tally,
    val pending: Tally,
    val unknownTotal: Int,
)

data class CategoryGroup(
    val kind: String,
    val key: String,
    val label: String,
    val categories: Int,
    val gross: Tally,
    val shareBasisPoints: Int?,
    val filter: Map<String, List<String>>,
)

data class DayTotal(val date: String, val gross: Tally, val refunds: Tally?, val coverage: String)

data class Comparison(
    val available: Boolean,
    val reason: String,
    val currentFrom: String,
    val currentTo: String,
    val current: Money,
    val previousFrom: String,
    val previousTo: String,
    val previous: Money,
    val difference: Money?,
    val percentBasisPoints: Int?,
)

data class Coverage(
    val complete: Boolean,
    val limitations: List<String>,
    val trackingState: String,
    val trackingSince: Instant?,
    val inboxPending: Int,
)

data class Overview(
    val generatedAt: Instant?,
    val period: Period,
    val currency: String,
    val totals: Totals,
    val categories: List<CategoryGroup>,
    val days: List<DayTotal>,
    val comparison: Comparison,
    val coverage: Coverage,
)

data class CategoryRef(val code: String, val label: String, val status: String, val origin: String)

data class PaymentRef(val kind: String, val label: String, val cardLast4: String?)

data class ExpenseItem(
    val id: String,
    val description: String,
    val merchant: String?,
    val date: String,
    val time: String?,
    val amount: Money,
    val amountBasis: String,
    val nature: String,
    val status: String,
    val inTotal: Boolean,
    val classification: String,
    val category: CategoryRef?,
    val payment: PaymentRef?,
    val installments: Int?,
)

data class ExpenseList(
    val period: Period,
    val totals: Totals,
    val matching: Int,
    val items: List<ExpenseItem>,
    val days: Map<String, Tally>,
    val nextOffset: Int?,
)

data class InstallmentPlan(
    val modality: String,
    val count: Int,
    val installmentAmount: Money?,
    val total: Money?,
    val charged: Int,
    val knownFuture: Int,
    val estimated: Int,
)

data class LinkedNotification(val notificationId: String, val version: Int, val event: String, val postedAt: Instant?)

data class Refund(val id: String, val amount: Money, val date: String?, val status: String)

data class ExpenseDetail(
    val item: ExpenseItem,
    val datePrecision: String,
    val payee: String?,
    val purpose: String?,
    val plan: InstallmentPlan?,
    val notifications: List<LinkedNotification>,
    val onStatement: Boolean,
    val refunds: List<Refund>,
    val refunded: Money,
    val openQuestions: Int,
    val lastChangeAt: Instant?,
    val lastChangeAuthor: String?,
)

data class MonthTotal(val month: String, val from: String, val to: String, val inProgress: Boolean, val gross: Tally, val coverage: String)

data class CategoryChange(val code: String, val label: String, val current: Money, val previous: Money, val difference: Money)

data class Analysis(
    val generatedAt: Instant?,
    val currency: String,
    val historyStartsOn: String?,
    val months: List<MonthTotal>,
    val sameDays: Comparison,
    val categories: List<CategoryChange>,
    val provisional: Int,
    val uncategorized: Int,
)

data class Whoami(val scopes: List<String>) {
    val canRead: Boolean get() = "expenses:read" in scopes || "admin" in scopes
    val canSend: Boolean get() = "wallet:ingest" in scopes || "admin" in scopes
}

object AppJson {
    fun overview(body: JSONObject): Overview = Overview(
        generatedAt = instant(body.optString("generated_at")),
        period = period(body.obj("period")),
        currency = body.optString("currency"),
        totals = totals(body.obj("totals")),
        categories = body.objects("categories").map(::group),
        days = body.objects("days").map(::day),
        comparison = comparison(body.obj("comparison")),
        coverage = coverage(body.obj("coverage")),
    )

    fun expenses(body: JSONObject): ExpenseList = ExpenseList(
        period = period(body.obj("period")),
        totals = totals(body.obj("totals")),
        matching = body.optInt("matching"),
        items = body.objects("items").map(::item),
        days = body.objects("days").associate { it.optString("date") to tally(it.obj("gross")) },
        nextOffset = if (body.isNull("next_offset")) null else body.optInt("next_offset"),
    )

    fun detail(body: JSONObject): ExpenseDetail = ExpenseDetail(
        item = item(body).copy(date = body.optString("date")),
        datePrecision = body.optString("date_precision"),
        payee = body.text("payee"),
        purpose = body.text("purpose"),
        plan = body.optJSONObject("installment_plan")?.let { plan ->
            InstallmentPlan(
                modality = plan.optString("modality"),
                count = plan.optInt("count"),
                installmentAmount = plan.optJSONObject("installment_amount")?.let(::money),
                total = plan.optJSONObject("total")?.let(::money),
                charged = plan.optInt("charged"),
                knownFuture = plan.optInt("known_future"),
                estimated = plan.optInt("estimated"),
            )
        },
        notifications = body.objects("notifications").map {
            LinkedNotification(it.optString("notification_id"), it.optInt("version"), it.optString("event"), instant(it.optString("posted_at")))
        },
        onStatement = body.optBoolean("on_statement"),
        refunds = body.objects("refunds").map { Refund(it.optString("id"), money(it.obj("amount")), it.text("date"), it.optString("status")) },
        refunded = money(body.obj("refunded")),
        openQuestions = body.optInt("open_questions"),
        lastChangeAt = body.optJSONObject("last_change")?.let { instant(it.optString("at")) },
        lastChangeAuthor = body.optJSONObject("last_change")?.optString("author"),
    )

    fun analysis(body: JSONObject): Analysis = Analysis(
        generatedAt = instant(body.optString("generated_at")),
        currency = body.optString("currency"),
        historyStartsOn = body.text("history_starts_on"),
        months = body.objects("months").map {
            MonthTotal(it.optString("month"), it.optString("from"), it.optString("to"), it.optBoolean("in_progress"), tally(it.obj("gross")), it.optString("coverage"))
        },
        sameDays = comparison(body.obj("same_days")),
        categories = body.objects("categories").map {
            CategoryChange(it.optString("code"), it.optString("label"), money(it.obj("current")), money(it.obj("previous")), money(it.obj("difference")))
        },
        provisional = body.obj("unconcluded").optInt("provisional"),
        uncategorized = body.obj("unconcluded").optInt("uncategorized"),
    )

    fun whoami(body: JSONObject): Whoami {
        val scopes = body.obj("api_key").optJSONArray("scopes") ?: JSONArray()
        return Whoami((0 until scopes.length()).map { scopes.optString(it) })
    }

    private fun period(o: JSONObject) = Period(
        month = o.optString("month"),
        from = o.optString("from"),
        to = o.optString("to"),
        monthEnd = o.optString("month_end"),
        inProgress = o.optBoolean("in_progress"),
        timezone = o.optString("timezone"),
    )

    private fun totals(o: JSONObject) = Totals(
        gross = tally(o.obj("gross")),
        average = o.optJSONObject("average")?.let(::money),
        refunds = tally(o.obj("refunds")),
        net = money(o.obj("net")),
        provisional = tally(o.obj("provisional")),
        pending = tally(o.obj("pending")),
        unknownTotal = o.optInt("unknown_total"),
    )

    private fun group(o: JSONObject): CategoryGroup {
        val filter = o.obj("filter")
        return CategoryGroup(
            kind = o.optString("kind"),
            key = o.optString("key"),
            label = o.optString("label"),
            categories = o.optInt("categories"),
            gross = tally(o.obj("gross")),
            shareBasisPoints = if (o.isNull("share_basis_points")) null else o.optInt("share_basis_points"),
            filter = filter.keys().asSequence().associateWith { key -> filter.strings(key) },
        )
    }

    private fun day(o: JSONObject) = DayTotal(
        date = o.optString("date"),
        gross = tally(o.obj("gross")),
        refunds = o.optJSONObject("refunds")?.let(::tally),
        coverage = o.optString("coverage"),
    )

    private fun comparison(o: JSONObject) = Comparison(
        available = o.optBoolean("available"),
        reason = o.optString("reason"),
        currentFrom = o.obj("current").optString("from"),
        currentTo = o.obj("current").optString("to"),
        current = money(o.obj("current").obj("gross")),
        previousFrom = o.obj("previous").optString("from"),
        previousTo = o.obj("previous").optString("to"),
        previous = money(o.obj("previous").obj("gross")),
        difference = o.optJSONObject("difference")?.let(::money),
        percentBasisPoints = if (o.has("percent_basis_points") && !o.isNull("percent_basis_points")) o.optInt("percent_basis_points") else null,
    )

    private fun coverage(o: JSONObject) = Coverage(
        complete = o.optBoolean("complete", true),
        limitations = o.strings("limitations"),
        trackingState = o.obj("tracking").optString("state"),
        trackingSince = instant(o.obj("tracking").optString("since")),
        inboxPending = o.obj("inbox").optInt("pending"),
    )

    private fun item(o: JSONObject) = ExpenseItem(
        id = o.optString("id"),
        description = o.optString("description"),
        merchant = o.text("merchant"),
        date = o.optString("date"),
        time = o.text("time"),
        amount = money(o.obj("amount")),
        amountBasis = o.optString("amount_basis"),
        nature = o.optString("nature"),
        status = o.optString("status"),
        inTotal = o.optBoolean("in_total"),
        classification = o.optString("classification"),
        category = o.optJSONObject("category")?.let {
            CategoryRef(it.optString("code"), it.optString("label"), it.optString("status"), it.optString("origin"))
        },
        payment = o.optJSONObject("payment")?.let { PaymentRef(it.optString("kind"), it.optString("label"), it.text("card_last4")) },
        installments = if (o.has("installments") && !o.isNull("installments")) o.optInt("installments") else null,
    )

    private fun tally(o: JSONObject) = Tally(money(o.obj("amount")), o.optInt("count"))

    private fun money(o: JSONObject) = Money(o.optLong("minor"), o.optString("currency"), o.optString("formatted"))

    private fun instant(value: String): Instant? = try {
        if (value.isEmpty()) null else Instant.parse(value)
    } catch (_: DateTimeParseException) {
        null
    }

    private fun JSONObject.obj(key: String): JSONObject = optJSONObject(key) ?: JSONObject()

    private fun JSONObject.objects(key: String): List<JSONObject> {
        val array = optJSONArray(key) ?: return emptyList()
        return (0 until array.length()).mapNotNull { array.optJSONObject(it) }
    }

    private fun JSONObject.strings(key: String): List<String> {
        val array = optJSONArray(key) ?: return emptyList()
        return (0 until array.length()).map { array.optString(it) }
    }

    private fun JSONObject.text(key: String): String? = if (isNull(key)) null else optString(key).takeIf { it.isNotEmpty() }
}
