package dev.code04.lastro.history

import dev.code04.lastro.net.CaseReading

object Labels {
    fun status(status: ItemStatus): String = when (status) {
        ItemStatus.OnlyOnPhone -> "Só no celular"
        ItemStatus.Queued -> "Na fila de envio"
        ItemStatus.TrackingPaused -> "Não guardada: acompanhamento pausado"
        ItemStatus.Discarded -> "Descartada pelo servidor"
        is ItemStatus.Refused -> "Recusada pelo servidor"
        ItemStatus.Sent -> "Enviada"
        ItemStatus.AwaitingCase -> "Aguardando a Case"
        is ItemStatus.Processed -> reading(status.reading)
        ItemStatus.Unknown -> "Sem registro de envio"
    }

    fun details(status: ItemStatus): String? = when (status) {
        is ItemStatus.Processed -> listOfNotNull(status.reading.category, status.reading.transactionStatus?.let(::transactionStatus))
            .joinToString(" · ")
            .ifEmpty { null }
        is ItemStatus.Refused -> status.reason.ifEmpty { null }
        else -> null
    }

    fun reading(reading: CaseReading): String = when (reading.effect) {
        "created" -> "Compra registrada"
        "linked" -> "Vinculada a um lançamento"
        "updated" -> "Compra atualizada"
        "voided" -> "Compra anulada"
        "refund_created" -> "Estorno registrado"
        "undone" -> "Leitura desfeita"
        else -> when (reading.eventType) {
            "declined" -> "Pagamento recusado"
            "notice" -> "Aviso, sem lançamento"
            "unrelated" -> "Sem relação com gastos"
            "unknown" -> "Dúvida aberta"
            else -> "Processada, sem efeito"
        }
    }

    fun transactionStatus(status: String): String = when (status) {
        "provisional" -> "provisória"
        "confirmed" -> "confirmada"
        "pending_clarification" -> "com dúvida"
        "voided" -> "anulada"
        "merged" -> "unida a outra compra"
        else -> status
    }
}
