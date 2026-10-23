package dev.code04.lastro

import dev.code04.lastro.capture.Redaction
import org.junit.Assert.assertEquals
import org.junit.Test

class RedactionTest {
    @Test
    fun fullCardNumbersKeepOnlyTheLastFourDigits() {
        assertEquals("Cartão **** **** **** 1111 aprovado", Redaction.redact("Cartão 4111 1111 1111 1111 aprovado"))
        assertEquals("Cartão ************1111", Redaction.redact("Cartão 4111111111111111"))
    }

    @Test
    fun numbersThatAreNotCardsStay() {
        val text = "Compra de R$ 32,97 com Visa •••• 1234, pedido 1234567890123"
        assertEquals(text, Redaction.redact(text))
    }

    @Test
    fun authenticationCodesAreRemoved() {
        assertEquals("Use o código [redacted] para confirmar", Redaction.redact("Use o código 482913 para confirmar"))
        assertEquals("Seu código de verificação é [redacted]", Redaction.redact("Seu código de verificação é 482913"))
        assertEquals("OTP: [redacted]", Redaction.redact("OTP: 9F3K21"))
    }
}
