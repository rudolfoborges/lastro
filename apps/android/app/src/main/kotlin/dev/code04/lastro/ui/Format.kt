package dev.code04.lastro.ui

import dev.code04.lastro.net.Money
import java.time.LocalDate
import java.time.YearMonth
import java.time.format.DateTimeParseException
import kotlin.math.abs

object Format {
    const val HIDDEN = "R$ ••••"

    private val months = listOf(
        "janeiro", "fevereiro", "março", "abril", "maio", "junho",
        "julho", "agosto", "setembro", "outubro", "novembro", "dezembro",
    )
    private val weekdays = listOf("segunda", "terça", "quarta", "quinta", "sexta", "sábado", "domingo")

    fun money(value: Money, hidden: Boolean): String = if (hidden) HIDDEN else value.formatted

    fun magnitude(value: Money, hidden: Boolean): String = if (hidden) HIDDEN else value.formatted.removePrefix("-")

    fun monthTitle(month: String): String = yearMonth(month)?.let { "${monthName(it).replaceFirstChar(Char::uppercase)} ${it.year}" } ?: month

    fun monthShort(month: String): String = yearMonth(month)?.let { monthName(it).take(3) } ?: month

    fun monthName(month: String): String = yearMonth(month)?.let(::monthName) ?: month

    private fun monthName(month: YearMonth): String = months[month.monthValue - 1]

    fun shortDate(date: String): String = localDate(date)?.let { "%02d/%02d".format(it.dayOfMonth, it.monthValue) } ?: date

    fun fullDate(date: String): String = localDate(date)?.let { "%02d/%02d/%d".format(it.dayOfMonth, it.monthValue, it.year) } ?: date

    fun weekday(date: String): String = localDate(date)?.let { weekdays[it.dayOfWeek.value - 1] } ?: ""

    fun dayHeading(date: String, today: LocalDate): String {
        val day = localDate(date) ?: return date
        val weekday = weekdays[day.dayOfWeek.value - 1]
        return when (day) {
            today -> "Hoje, $weekday ${shortDate(date)}"
            today.minusDays(1) -> "Ontem, $weekday ${shortDate(date)}"
            else -> "${weekday.replaceFirstChar(Char::uppercase)}, ${shortDate(date)}"
        }
    }

    fun dayRange(from: String, to: String): String {
        val first = localDate(from) ?: return "$from a $to"
        val last = localDate(to) ?: return "$from a $to"
        val month = months[last.monthValue - 1]
        return if (first.month == last.month) "${first.dayOfMonth} a ${last.dayOfMonth} de $month" else "${shortDate(from)} a ${shortDate(to)}"
    }

    fun percent(basisPoints: Int): String {
        val tenths = Math.round(abs(basisPoints) / 10.0)
        val sign = if (basisPoints < 0) "-" else ""
        return "$sign${tenths / 10},${tenths % 10}%"
    }

    fun signedPercent(basisPoints: Int): String = if (basisPoints >= 0) "+" + percent(basisPoints) else percent(basisPoints)

    fun count(value: Int, one: String, many: String): String = "$value ${if (value == 1) one else many}"

    fun yearMonth(month: String): YearMonth? = try {
        YearMonth.parse(month)
    } catch (_: DateTimeParseException) {
        null
    }

    fun localDate(date: String): LocalDate? = try {
        LocalDate.parse(date)
    } catch (_: DateTimeParseException) {
        null
    }
}
