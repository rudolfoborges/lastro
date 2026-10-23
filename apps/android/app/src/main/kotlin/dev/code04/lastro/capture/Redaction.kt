package dev.code04.lastro.capture

object Redaction {
    const val PLACEHOLDER = "[redacted]"

    private val cardCandidate = Regex("""(?<![\d*])\d(?:[ -]?\d){12,18}(?!\d)""")
    private val labeledSecret = Regex(
        """(?i)\b(c[oó]digo|code|otp|token|senha|password|passcode|pin|cvv|cvc)\b((?:\s+[^\s\d]{1,12}){0,3}\s*[:=]?\s*)([A-Za-z0-9-]*\d[A-Za-z0-9-]*)""",
    )

    fun redact(text: String): String {
        val withoutSecrets = labeledSecret.replace(text) { match ->
            val value = match.groupValues[3]
            if (value.length < 3) match.value else match.groupValues[1] + match.groupValues[2] + PLACEHOLDER
        }
        return cardCandidate.replace(withoutSecrets) { match ->
            val digits = match.value.filter(Char::isDigit)
            if (digits.length in 13..19 && passesLuhn(digits)) maskAllButLast4(match.value) else match.value
        }
    }

    private fun maskAllButLast4(value: String): String {
        val total = value.count(Char::isDigit)
        var seen = 0
        return buildString {
            for (c in value) {
                if (c.isDigit()) {
                    seen++
                    append(if (seen <= total - 4) '*' else c)
                } else {
                    append(c)
                }
            }
        }
    }

    private fun passesLuhn(digits: String): Boolean {
        var sum = 0
        digits.reversed().forEachIndexed { index, c ->
            var d = c - '0'
            if (index % 2 == 1) {
                d *= 2
                if (d > 9) d -= 9
            }
            sum += d
        }
        return sum % 10 == 0
    }
}
