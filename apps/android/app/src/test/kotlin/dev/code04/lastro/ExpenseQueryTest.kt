package dev.code04.lastro

import dev.code04.lastro.net.CategoryGroup
import dev.code04.lastro.net.ExpenseQuery
import dev.code04.lastro.net.Money
import dev.code04.lastro.net.Tally
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ExpenseQueryTest {
    @Test
    fun aListOpenedFromABarCarriesTheBarFilter() {
        val group = CategoryGroup(
            kind = "other",
            key = "other",
            label = "Outras",
            categories = 2,
            gross = Tally(Money(21600, "BRL", "R$ 216,00"), 7),
            shareBasisPoints = 574,
            filter = mapOf(
                "from" to listOf("2026-10-01"),
                "to" to listOf("2026-10-25"),
                "currency" to listOf("BRL"),
                "status" to listOf("confirmed"),
                "nature" to listOf("expense"),
                "classification" to listOf("confirmed"),
                "category" to listOf("leisure", "transport"),
            ),
        )

        val query = ExpenseQuery.ofGroup(group, "Visão geral › Por categoria")

        assertEquals(
            "/v1/expenses?from=2026-10-01&to=2026-10-25&currency=BRL&status=confirmed&nature=expense&category=leisure&category=transport&classification=confirmed&limit=50",
            query.path(),
        )
        assertEquals("Outras", query.label)
    }

    @Test
    fun textAndPagesAreEncoded() {
        val query = ExpenseQuery(month = "2026-10", text = " pão & café ")

        assertEquals("/v1/expenses?month=2026-10&currency=BRL&q=p%C3%A3o+%26+caf%C3%A9&limit=50&offset=50", query.path(offset = 50))
        assertTrue(query.filtered)
    }

    @Test
    fun clearingKeepsOnlyTheMonth() {
        val cleared = ExpenseQuery(from = "2026-10-01", to = "2026-10-25", statuses = listOf("confirmed"), label = "Mercado").cleared()

        assertEquals("2026-10", cleared.month)
        assertFalse(cleared.filtered)
        assertEquals("/v1/expenses?month=2026-10&currency=BRL&limit=50", cleared.path())
    }
}
