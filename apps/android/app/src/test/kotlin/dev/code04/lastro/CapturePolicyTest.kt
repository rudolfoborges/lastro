package dev.code04.lastro

import dev.code04.lastro.capture.CapturePolicy
import dev.code04.lastro.capture.GOOGLE_WALLET
import dev.code04.lastro.capture.NotificationEvent
import dev.code04.lastro.capture.RawNotification
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Test

class CapturePolicyTest {
    private val allowlist = setOf(GOOGLE_WALLET)

    private fun wallet(text: String, postTime: Long = 1_000L) =
        RawNotification(GOOGLE_WALLET, "0|wallet|7|null|10123", postTime, "Padaria Pão Quente", text, null)

    @Test
    fun notificationsFromAppsOutsideTheListNeverLeaveThePhone() {
        val chat = RawNotification("com.whatsapp", "0|com.whatsapp|1|null|10123", 1_000L, "Ana", "Oi, tudo bem?", null)

        assertNull(CapturePolicy.capture(chat, allowlist, previousHash = null))
        assertNull(CapturePolicy.removal("com.whatsapp", chat.key, allowlist, previousHash = "abc"))
    }

    @Test
    fun aWalletNotificationIsPostedThenUpdated() {
        val first = CapturePolicy.capture(wallet("Compra de R$ 32,97 pendente"), allowlist, previousHash = null)!!
        val repeated = CapturePolicy.capture(wallet("Compra de R$ 32,97 pendente"), allowlist, previousHash = first.contentHash)
        val update = CapturePolicy.capture(wallet("Compra de R$ 32,97 aprovada", 2_000L), allowlist, previousHash = first.contentHash)!!

        assertEquals(NotificationEvent.POSTED, first.event)
        assertNull(repeated)
        assertEquals(NotificationEvent.UPDATED, update.event)
        assertNotEquals(first.contentHash, update.contentHash)
    }

    @Test
    fun textIsRedactedBeforeItIsKept() {
        val captured = CapturePolicy.capture(wallet("Cartão 4111 1111 1111 1111, código 482913"), allowlist, previousHash = null)!!

        assertEquals("Cartão **** **** **** 1111, código [redacted]", captured.text)
    }

    @Test
    fun emptyNotificationsAreIgnored() {
        val empty = RawNotification(GOOGLE_WALLET, "k", 1_000L, " ", null, "")

        assertNull(CapturePolicy.capture(empty, allowlist, previousHash = null))
    }

    @Test
    fun onlyKnownNotificationsAreReportedAsRemoved() {
        assertNull(CapturePolicy.removal(GOOGLE_WALLET, "k", allowlist, previousHash = null))
        assertEquals(NotificationEvent.REMOVED, CapturePolicy.removal(GOOGLE_WALLET, "k", allowlist, previousHash = "abc")?.event)
    }
}
