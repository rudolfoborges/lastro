package dev.code04.lastro.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.code04.lastro.R
import dev.code04.lastro.history.CollectionStages
import dev.code04.lastro.history.CollectionSummary
import dev.code04.lastro.history.HistoryItem
import dev.code04.lastro.history.ItemStatus
import dev.code04.lastro.history.Outcome
import dev.code04.lastro.history.Stage
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter

const val COLLECTION_WINDOW_DAYS = 7L

fun collectionSince(now: Long = System.currentTimeMillis()): Long = now - COLLECTION_WINDOW_DAYS * 24 * 60 * 60 * 1000

fun collectionGlance(snapshot: StatusSnapshot, summary: CollectionSummary?): CollectionGlance = when {
    !snapshot.notificationAccess -> CollectionGlance("Coleta sem acesso às notificações", "Sem acesso", Tone.BAD, summary)
    snapshot.paused -> CollectionGlance("Coleta pausada", "Pausada", Tone.WAITING, summary)
    !snapshot.hasDestination || !snapshot.sendingEnabled -> CollectionGlance("Envio ao Lastro desligado", "Envio desligado", Tone.WAITING, summary)
    else -> CollectionGlance("Coleta ativa", "Coleta ativa", Tone.GOOD, summary)
}

@Composable
fun CollectionScreen(
    snapshot: StatusSnapshot,
    items: List<HistoryItem>,
    server: ServerView,
    onOpenExpense: (String) -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var filter by rememberSaveable { mutableStateOf<Stage?>(null) }
    val since = collectionSince()
    val window = items.filter { it.capture.capturedAt >= since }
    val summary = CollectionStages.summarize(items, since)
    val shown = window.filter { filter == null || CollectionStages.stage(it.status) == filter }
    LazyColumn(
        modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 4.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item { PathCard(snapshot, summary, server, onOpenSettings) }
        item { CountsCard(summary) }
        if (summary.blocked > 0) item { BlockedNotice(window, onOpenSettings) }
        item {
            Text("Notificações", Modifier.padding(start = 4.dp, top = 4.dp), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
        }
        item {
            LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                item { FilterChip(selected = filter == null, onClick = { filter = null }, label = { Text("Todas") }) }
                items(Stage.entries.filter { it != Stage.UNAVAILABLE }) { stage ->
                    FilterChip(selected = filter == stage, onClick = { filter = if (filter == stage) null else stage }, label = { Text(CollectionStages.stageLabel(stage)) })
                }
            }
        }
        if (shown.isEmpty()) {
            item {
                Notice(
                    if (window.isEmpty()) {
                        "Nenhuma notificação capturada nos últimos 7 dias. As compras do Google Wallet aparecem aqui assim que chegam ao celular."
                    } else {
                        "Nenhuma notificação nesta situação nos últimos 7 dias."
                    },
                    Tone.NEUTRAL,
                    R.drawable.ic_bell,
                )
            }
        }
        shown.groupBy { localDay(it) }.forEach { (day, dayItems) ->
            item(key = "day-$day") {
                Text(Format.dayHeading(day.toString(), LocalDate.now()), Modifier.padding(start = 4.dp, top = 4.dp), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            item(key = "list-$day") {
                Column(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainer, RoundedCornerShape(16.dp))) {
                    dayItems.forEachIndexed { index, item ->
                        if (index > 0) HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                        NotificationRow(item, onOpenExpense)
                    }
                }
            }
        }
        item {
            Text(
                "Coleta ativa e fila vazia não garantem que todas as compras foram registradas. Compras fora do Google Wallet chegam pela conversa com a Case.",
                Modifier.padding(horizontal = 4.dp),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

private fun localDay(item: HistoryItem): LocalDate {
    val at = item.capture.postedAt.takeIf { it > 0 } ?: item.capture.capturedAt
    return Instant.ofEpochMilli(at).atZone(ZoneId.systemDefault()).toLocalDate()
}

@Composable
private fun PathCard(snapshot: StatusSnapshot, summary: CollectionSummary, server: ServerView, onOpenSettings: () -> Unit) {
    val glance = collectionGlance(snapshot, summary)
    SectionCard(title = null) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Caminho das notificações", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            StatusChip(glance.chip, glance.tone, if (glance.tone == Tone.GOOD) R.drawable.ic_check else R.drawable.ic_alert)
        }
        Row(verticalAlignment = Alignment.Top) {
            val captureTone = when {
                !snapshot.notificationAccess -> Tone.BAD
                snapshot.paused -> Tone.WAITING
                else -> Tone.GOOD
            }
            Stage(
                R.drawable.ic_phone,
                "Captura",
                when {
                    !snapshot.notificationAccess -> "sem acesso"
                    snapshot.paused -> "pausada"
                    else -> "no celular"
                },
                captureTone,
            )
            Connector(captureTone)
            val sendTone = when {
                !snapshot.hasDestination || !snapshot.sendingEnabled -> Tone.BAD
                snapshot.queueSize > 0 -> Tone.WAITING
                else -> Tone.GOOD
            }
            Stage(
                R.drawable.ic_upload,
                "Envio",
                when {
                    !snapshot.hasDestination -> "sem destino"
                    !snapshot.sendingEnabled -> "desligado"
                    snapshot.queueSize > 0 -> "${snapshot.queueSize} na fila"
                    else -> "fila vazia"
                },
                sendTone,
            )
            Connector(sendTone)
            val caseTone = when {
                server.problem.isNotEmpty() -> Tone.NEUTRAL
                summary.awaitingResult > 0 -> Tone.WAITING
                else -> Tone.GOOD
            }
            Stage(
                R.drawable.ic_chat,
                "Processamento",
                when {
                    server.problem.isNotEmpty() -> "sem consulta"
                    summary.awaitingResult > 0 -> "${summary.awaitingResult} aguardando"
                    else -> "em dia"
                },
                caseTone,
            )
        }
        Text(serverLine(server), style = MaterialTheme.typography.bodySmall, color = if (server.problem.isEmpty()) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.error)
        if (snapshot.lastError.isNotEmpty()) Text(snapshot.lastError, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        if (glance.tone != Tone.GOOD) TonalButton("Abrir Configurações", onOpenSettings)
    }
}

fun serverLine(server: ServerView): String = when {
    server.problem.isNotEmpty() -> server.problem
    server.checkedAt > 0 -> "Situação na Case consultada às ${formatClock(Instant.ofEpochMilli(server.checkedAt))}."
    server.checking -> "Consultando a situação na Case…"
    else -> "Situação na Case ainda não consultada."
}

@Composable
private fun androidx.compose.foundation.layout.RowScope.Stage(icon: Int, title: String, value: String, tone: Tone) {
    Column(Modifier.weight(1f).semantics(mergeDescendants = true) {}, horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(6.dp)) {
        IconCircle(icon, tone, 44.dp)
        Text(title, style = MaterialTheme.typography.labelLarge, fontWeight = FontWeight.SemiBold)
        Text(value, style = MaterialTheme.typography.bodySmall, color = if (tone == Tone.GOOD) MaterialTheme.colorScheme.onSurfaceVariant else tone.accent(), textAlign = TextAlign.Center)
    }
}

@Composable
private fun Connector(tone: Tone) {
    Box(
        Modifier.padding(top = 21.dp).size(width = 16.dp, height = 2.dp).background(
            if (tone == Tone.GOOD) MaterialTheme.colorScheme.primary.copy(alpha = 0.6f) else MaterialTheme.colorScheme.outline,
        ),
    )
}

@Composable
private fun CountsCard(summary: CollectionSummary) {
    val scheme = MaterialTheme.colorScheme
    SectionCard(title = null) {
        Text("Capturadas nos últimos 7 dias", style = MaterialTheme.typography.labelLarge, color = scheme.onSurfaceVariant)
        Text(Format.count(summary.captured, "notificação capturada", "notificações capturadas"), style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
        Text("Situação atual de cada uma, pela data de captura. Notificações não são despesas.", style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
        StageSegments(
            listOf(
                summary.processed to { m: Modifier -> Box(m.background(scheme.primary)) },
                summary.awaitingResult to { m: Modifier -> Box(m.background(scheme.tertiaryContainer).stripes(scheme.tertiary, gap = 5.dp)) },
                summary.awaitingSend to { m: Modifier -> Box(m.background(scheme.tertiary)) },
                summary.blocked to { m: Modifier -> Box(m.background(scheme.error)) },
                summary.unavailable to { m: Modifier -> Box(m.background(scheme.outline)) },
            ),
            "${summary.processed} processadas, ${summary.awaitingResult} aguardando resultado, ${summary.awaitingSend} aguardando envio, ${summary.blocked} com impedimento, ${summary.unavailable} com situação indisponível. Total de ${summary.captured}.",
        )
        CountRow(summary.processed, "Processadas", "com resultado da Case", { SolidSwatch(scheme.primary) })
        CountRow(summary.awaitingResult, "Aguardando resultado", "enviadas; a Case ainda não leu", { StripedSwatch() })
        CountRow(summary.awaitingSend, "Aguardando envio", "ainda no celular", { SolidSwatch(scheme.tertiary) })
        CountRow(summary.blocked, "Com impedimento", "não seguem; motivo abaixo", { SolidSwatch(scheme.error) })
        if (summary.unavailable > 0) CountRow(summary.unavailable, "Situação indisponível", "enviadas antes da consulta atual", { SolidSwatch(scheme.outline) })
        HorizontalDivider(color = scheme.outlineVariant)
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Enviadas até agora, já contando as processadas. Não some às situações acima.", Modifier.weight(1f).padding(end = 12.dp), style = MaterialTheme.typography.bodySmall, color = scheme.onSurfaceVariant)
            Text(summary.sent.toString(), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
        }
        if (summary.processed > 0) {
            Column(Modifier.fillMaxWidth().background(scheme.surfaceContainerHigh, RoundedCornerShape(14.dp)).padding(12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text("Resultado das ${summary.processed} processadas", style = MaterialTheme.typography.labelLarge)
                InfoRow("originaram uma despesa", summary.becameExpense.toString(), scheme.onSurface)
                InfoRow("eram da mesma despesa de outra notificação", summary.sameExpense.toString(), scheme.onSurface)
                InfoRow("não representavam gasto", summary.noExpense.toString(), scheme.onSurface)
                if (summary.doubts > 0) InfoRow("viraram dúvida para você responder", summary.doubts.toString(), scheme.onSurface)
            }
        }
    }
}

@Composable
private fun CountRow(count: Int, title: String, detail: String, swatch: @Composable () -> Unit) {
    Row(Modifier.fillMaxWidth().heightIn(min = 44.dp).semantics(mergeDescendants = true) {}, verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        swatch()
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Medium)
            Text(detail, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Text(count.toString(), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun BlockedNotice(window: List<HistoryItem>, onOpenSettings: () -> Unit) {
    val blocked = window.filter { CollectionStages.stage(it.status) == Stage.BLOCKED }
    val reasons = blocked.groupingBy { CollectionStages.explanation(it.status) }.eachCount()
    val needsSettings = blocked.any { it.status == ItemStatus.OnlyOnPhone }
    Notice(
        text = reasons.entries.joinToString(" ") { (reason, count) -> "$count: ${reason.replaceFirstChar(Char::uppercase)}." } +
            " As próximas notificações seguem normalmente. Se era uma compra, conte à Case.",
        tone = Tone.BAD,
        icon = R.drawable.ic_alert,
        title = Format.count(blocked.size, "notificação com impedimento", "notificações com impedimento"),
    ) { if (needsSettings) TonalButton("Abrir Configurações", onOpenSettings) }
}

private val clock = DateTimeFormatter.ofPattern("HH:mm").withZone(ZoneId.systemDefault())

@Composable
private fun NotificationRow(item: HistoryItem, onOpenExpense: (String) -> Unit) {
    val hidden = LocalHidden.current
    val stage = CollectionStages.stage(item.status)
    val processed = item.status as? ItemStatus.Processed
    val transactionId = processed?.reading?.transactionId?.takeIf { processed.reading.let(CollectionStages::outcome) != Outcome.NO_EXPENSE }
    val (icon, tone) = when (stage) {
        Stage.AWAITING_SEND -> R.drawable.ic_upload to Tone.WAITING
        Stage.AWAITING_RESULT -> R.drawable.ic_clock to Tone.WAITING
        Stage.PROCESSED -> when (processed?.let { CollectionStages.outcome(it.reading) }) {
            Outcome.SAME_EXPENSE -> R.drawable.ic_link to Tone.GOOD
            Outcome.NO_EXPENSE -> R.drawable.ic_minus to Tone.NEUTRAL
            Outcome.DOUBT -> R.drawable.ic_info to Tone.WAITING
            else -> R.drawable.ic_check to Tone.GOOD
        }
        Stage.BLOCKED -> R.drawable.ic_alert to Tone.BAD
        Stage.UNAVAILABLE -> R.drawable.ic_minus to Tone.NEUTRAL
    }
    val capture = item.capture
    val base = Modifier.fillMaxWidth().heightIn(min = 72.dp)
    Row(
        (if (transactionId != null) base.tappable("Ver despesa") { onOpenExpense(transactionId) } else base).padding(horizontal = 16.dp, vertical = 14.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        IconCircle(icon, tone)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Row(verticalAlignment = Alignment.Bottom) {
                Text(
                    if (hidden) "Notificação do Google Wallet" else capture.title.ifEmpty { "Google Wallet" },
                    Modifier.weight(1f),
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.SemiBold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(clock.format(Instant.ofEpochMilli(capture.postedAt.takeIf { it > 0 } ?: capture.capturedAt)), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Text(
                if (hidden) "texto oculto" else capture.bigText.ifEmpty { capture.text }.ifEmpty { "texto indisponível" },
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Row {
                Text(
                    CollectionStages.stageLabel(stage),
                    style = MaterialTheme.typography.labelLarge,
                    color = if (tone == Tone.NEUTRAL) MaterialTheme.colorScheme.onSurface else tone.accent(),
                )
                Text(" · " + CollectionStages.explanation(item.status), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
        if (transactionId != null) Box(Modifier.align(Alignment.CenterVertically)) { ChevronIcon() }
    }
}
