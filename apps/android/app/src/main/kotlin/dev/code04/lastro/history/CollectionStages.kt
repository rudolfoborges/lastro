package dev.code04.lastro.history

import dev.code04.lastro.net.CaseReading

enum class Stage { AWAITING_SEND, AWAITING_RESULT, PROCESSED, BLOCKED, UNAVAILABLE }

enum class Outcome { BECAME_EXPENSE, SAME_EXPENSE, NO_EXPENSE, DOUBT }

data class CollectionSummary(
    val captured: Int,
    val awaitingSend: Int,
    val awaitingResult: Int,
    val processed: Int,
    val blocked: Int,
    val unavailable: Int,
    val sent: Int,
    val becameExpense: Int,
    val sameExpense: Int,
    val noExpense: Int,
    val doubts: Int,
)

object CollectionStages {
    fun stage(status: ItemStatus): Stage = when (status) {
        ItemStatus.Queued -> Stage.AWAITING_SEND
        ItemStatus.AwaitingCase -> Stage.AWAITING_RESULT
        is ItemStatus.Processed -> Stage.PROCESSED
        ItemStatus.OnlyOnPhone, ItemStatus.TrackingPaused, ItemStatus.Discarded, is ItemStatus.Refused -> Stage.BLOCKED
        ItemStatus.Sent, ItemStatus.Unknown -> Stage.UNAVAILABLE
    }

    fun outcome(reading: CaseReading): Outcome = when {
        reading.effect == "created" || reading.effect == "refund_created" -> Outcome.BECAME_EXPENSE
        reading.effect == "linked" || reading.effect == "updated" || reading.effect == "voided" -> Outcome.SAME_EXPENSE
        reading.eventType == "unknown" -> Outcome.DOUBT
        else -> Outcome.NO_EXPENSE
    }

    fun summarize(items: List<HistoryItem>, since: Long): CollectionSummary {
        val window = items.filter { it.capture.capturedAt >= since }
        val stages = window.map { stage(it.status) }
        val outcomes = window.mapNotNull { (it.status as? ItemStatus.Processed)?.let { processed -> outcome(processed.reading) } }
        return CollectionSummary(
            captured = window.size,
            awaitingSend = stages.count { it == Stage.AWAITING_SEND },
            awaitingResult = stages.count { it == Stage.AWAITING_RESULT },
            processed = stages.count { it == Stage.PROCESSED },
            blocked = stages.count { it == Stage.BLOCKED },
            unavailable = stages.count { it == Stage.UNAVAILABLE },
            sent = window.count { it.status == ItemStatus.AwaitingCase || it.status is ItemStatus.Processed || it.capture.delivery.reachedServer },
            becameExpense = outcomes.count { it == Outcome.BECAME_EXPENSE },
            sameExpense = outcomes.count { it == Outcome.SAME_EXPENSE },
            noExpense = outcomes.count { it == Outcome.NO_EXPENSE },
            doubts = outcomes.count { it == Outcome.DOUBT },
        )
    }

    fun stageLabel(stage: Stage): String = when (stage) {
        Stage.AWAITING_SEND -> "Aguardando envio"
        Stage.AWAITING_RESULT -> "Aguardando resultado"
        Stage.PROCESSED -> "Processada"
        Stage.BLOCKED -> "Com impedimento"
        Stage.UNAVAILABLE -> "Situação indisponível"
    }

    fun explanation(status: ItemStatus): String = when (status) {
        ItemStatus.Queued -> "ainda no celular"
        ItemStatus.AwaitingCase -> "enviada; a Case ainda não leu"
        is ItemStatus.Processed -> when (outcome(status.reading)) {
            Outcome.BECAME_EXPENSE -> listOfNotNull("originou despesa", status.reading.category).joinToString(" · ")
            Outcome.SAME_EXPENSE -> "da mesma despesa de outra notificação"
            Outcome.DOUBT -> "dúvida aberta; a Case vai perguntar a você"
            Outcome.NO_EXPENSE -> noExpense(status.reading)
        }
        ItemStatus.OnlyOnPhone -> "envio ao Lastro desligado quando chegou"
        ItemStatus.TrackingPaused -> "não guardada: acompanhamento pausado no Lastro"
        ItemStatus.Discarded -> "descartada pelo Lastro"
        is ItemStatus.Refused -> "envio recusado" + status.reason.takeIf { it.isNotEmpty() }?.let { ": $it" }.orEmpty()
        ItemStatus.Sent -> "enviada; resultado fora da consulta"
        ItemStatus.Unknown -> "sem registro de envio"
    }

    private fun noExpense(reading: CaseReading): String = when (reading.eventType) {
        "declined" -> "sem gasto: pagamento recusado"
        "notice" -> "sem gasto: aviso"
        "unrelated" -> "sem gasto: não é compra"
        else -> if (reading.effect == "undone") "leitura desfeita" else "sem gasto"
    }
}
