package dev.code04.lastro

import dev.code04.lastro.net.AppJson
import dev.code04.lastro.net.Money
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class AppJsonTest {
    private fun money(minor: Long, formatted: String) = """{"minor": $minor, "currency": "BRL", "formatted": "$formatted"}"""

    private fun tally(minor: Long, formatted: String, count: Int) = """{"amount": ${money(minor, formatted)}, "count": $count}"""

    @Test
    fun readsTheOverviewWithItsGroupsDaysAndComparison() {
        val body = JSONObject(
            """
            {"generated_at": "2026-10-25T11:14:00Z",
             "period": {"month": "2026-10", "from": "2026-10-01", "to": "2026-10-25", "month_end": "2026-10-31", "in_progress": true, "basis": "purchase_date", "timezone": "America/Sao_Paulo"},
             "currency": "BRL", "currencies": ["BRL"],
             "totals": {"gross": ${tally(376200, "R$ 3.762,00", 46)}, "average": ${money(8178, "R$ 81,78")}, "refunds": ${tally(8990, "R$ 89,90", 2)},
                        "net": ${money(367210, "R$ 3.672,10")}, "provisional": ${tally(31260, "R$ 312,60", 4)}, "pending": ${tally(0, "R$ 0,00", 0)}, "unknown_total": 1},
             "categories": [{"kind": "category", "key": "groceries", "label": "Mercado", "categories": 1, "gross": ${tally(128450, "R$ 1.284,50", 9)},
                             "share_basis_points": 3414, "filter": {"from": ["2026-10-01"], "to": ["2026-10-25"], "category": ["groceries"]}},
                            {"kind": "uncategorized", "key": "uncategorized", "label": "Sem categoria", "categories": 0, "gross": ${tally(0, "R$ 0,00", 0)},
                             "share_basis_points": null, "filter": {}}],
             "days": [{"date": "2026-10-01", "gross": ${tally(12640, "R$ 126,40", 2)}, "refunds": ${tally(0, "R$ 0,00", 0)}, "coverage": "partial"}],
             "comparison": {"available": true, "current": {"from": "2026-10-01", "to": "2026-10-25", "gross": ${money(376200, "R$ 3.762,00")}},
                            "previous": {"from": "2026-09-01", "to": "2026-09-25", "gross": ${money(341840, "R$ 3.418,40")}},
                            "difference": ${money(34360, "R$ 343,60")}, "percent_basis_points": 1005},
             "coverage": {"complete": false, "limitations": ["2 notificações ainda não foram interpretadas."], "tracking": {"state": "active", "since": "2026-10-24T13:00:00Z"}, "inbox": {"pending": 2}, "devices": []}}
            """,
        )

        val overview = AppJson.overview(body)

        assertEquals("2026-10", overview.period.month)
        assertTrue(overview.period.inProgress)
        assertEquals(Money(376200, "BRL", "R$ 3.762,00"), overview.totals.gross.amount)
        assertEquals(1, overview.totals.unknownTotal)
        assertEquals(3414, overview.categories[0].shareBasisPoints)
        assertEquals(listOf("groceries"), overview.categories[0].filter["category"])
        assertNull(overview.categories[1].shareBasisPoints)
        assertEquals("partial", overview.days[0].coverage)
        assertEquals(1005, overview.comparison.percentBasisPoints)
        assertEquals("2026-09-25", overview.comparison.previousTo)
        assertFalse(overview.coverage.complete)
        assertEquals(2, overview.coverage.inboxPending)
    }

    @Test
    fun anUnavailableComparisonHasNoDifference() {
        val comparison = AppJson.overview(
            JSONObject("""{"comparison": {"available": false, "reason": "no_history", "current": {}, "previous": {}}}"""),
        ).comparison

        assertFalse(comparison.available)
        assertEquals("no_history", comparison.reason)
        assertNull(comparison.difference)
        assertNull(comparison.percentBasisPoints)
    }

    @Test
    fun readsTheListWithItsStatusesAndPages() {
        val list = AppJson.expenses(
            JSONObject(
                """
                {"period": {"from": "2026-10-01", "to": "2026-10-25"}, "totals": {"gross": ${tally(16850, "R$ 168,50", 2)}}, "matching": 51,
                 "items": [{"id": "t1", "description": "Compra na farmácia", "merchant": "Farmácia Exemplo", "date": "2026-10-24", "time": "19:05",
                            "amount": ${money(4350, "R$ 43,50")}, "amount_basis": "total", "nature": "expense", "status": "confirmed", "in_total": true,
                            "classification": "suggested", "category": {"code": "health", "label": "Saúde", "status": "suggested"},
                            "payment": {"kind": "credit_card", "label": "cartão de crédito", "card_last4": "4321"}},
                           {"id": "t2", "description": "TV", "date": "2026-10-23", "amount": ${money(10000, "R$ 100,00")}, "amount_basis": "installment",
                            "nature": "expense", "status": "confirmed", "in_total": false, "classification": "missing", "installments": 12}],
                 "days": [{"date": "2026-10-24", "gross": ${tally(4350, "R$ 43,50", 1)}}], "next_offset": 50}
                """,
            ),
        )

        assertEquals(51, list.matching)
        assertEquals(50, list.nextOffset)
        assertEquals("Farmácia Exemplo", list.items[0].merchant)
        assertEquals("suggested", list.items[0].category?.status)
        assertEquals("4321", list.items[0].payment?.cardLast4)
        assertNull(list.items[1].category)
        assertEquals(12, list.items[1].installments)
        assertEquals(4350L, list.days["2026-10-24"]?.amount?.minor)
    }

    @Test
    fun readsTheDetailAndTheAnalysis() {
        val detail = AppJson.detail(
            JSONObject(
                """
                {"id": "t1", "description": "Farmácia", "date": "2026-10-24", "date_precision": "exact", "amount": ${money(4350, "R$ 43,50")},
                 "amount_basis": "total", "nature": "expense", "status": "provisional", "in_total": false, "classification": "suggested",
                 "notifications": [{"notification_id": "n1", "version": 1, "event": "posted", "posted_at": "2026-10-24T22:05:00Z"}],
                 "on_statement": false, "refunds": [], "refunded": ${money(0, "R$ 0,00")}, "open_questions": 1,
                 "last_change": {"at": "2026-10-24T22:12:00Z", "author": "agent"}}
                """,
            ),
        )
        val analysis = AppJson.analysis(
            JSONObject(
                """
                {"currency": "BRL", "history_starts_on": "2026-10-24",
                 "months": [{"month": "2026-09", "from": "2026-09-01", "to": "2026-09-30", "in_progress": false, "gross": ${tally(0, "R$ 0,00", 0)}, "coverage": "none"}],
                 "same_days": {"available": false, "reason": "no_history", "current": {}, "previous": {}},
                 "categories": [], "unconcluded": {"provisional": 3, "uncategorized": 2}}
                """,
            ),
        )

        assertEquals(1, detail.notifications.size)
        assertEquals("agent", detail.lastChangeAuthor)
        assertEquals(1, detail.openQuestions)
        assertEquals("none", analysis.months[0].coverage)
        assertEquals("2026-10-24", analysis.historyStartsOn)
        assertEquals(3, analysis.provisional)
    }

    @Test
    fun theWhoamiTellsWhatTheCredentialAllows() {
        val both = AppJson.whoami(JSONObject("""{"api_key": {"scopes": ["wallet:ingest", "expenses:read"]}}"""))
        val ingest = AppJson.whoami(JSONObject("""{"api_key": {"scopes": ["wallet:ingest"]}}"""))

        assertTrue(both.canRead && both.canSend)
        assertFalse(ingest.canRead)
    }
}
