package dev.code04.lastro

import dev.code04.lastro.net.Money
import dev.code04.lastro.ui.Format
import dev.code04.lastro.ui.thousands
import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.LocalDate

class FormatTest {
    @Test
    fun percentagesHaveOneDecimalAndASign() {
        assertEquals("34,1%", Format.percent(3414))
        assertEquals("100,0%", Format.percent(10000))
        assertEquals("+10,1%", Format.signedPercent(1005))
        assertEquals("-5,6%", Format.signedPercent(-555))
    }

    @Test
    fun hiddenValuesNeverShowTheAmount() {
        val value = Money(-5580, "BRL", "-R$ 55,80")

        assertEquals("R$ ••••", Format.money(value, hidden = true))
        assertEquals("R$ 55,80", Format.magnitude(value, hidden = false))
    }

    @Test
    fun datesAndMonthsAreWrittenInPortuguese() {
        val today = LocalDate.parse("2026-10-25")

        assertEquals("Outubro 2026", Format.monthTitle("2026-10"))
        assertEquals("Hoje, domingo 25/10", Format.dayHeading("2026-10-25", today))
        assertEquals("Ontem, sábado 24/10", Format.dayHeading("2026-10-24", today))
        assertEquals("Sexta, 23/10", Format.dayHeading("2026-10-23", today))
        assertEquals("1 a 25 de setembro", Format.dayRange("2026-09-01", "2026-09-25"))
        assertEquals("2 despesas", Format.count(2, "despesa", "despesas"))
    }

    @Test
    fun monthlyValuesAreShortened() {
        assertEquals("3,8 mil", thousands(376200))
        assertEquals("850", thousands(85000))
    }
}
