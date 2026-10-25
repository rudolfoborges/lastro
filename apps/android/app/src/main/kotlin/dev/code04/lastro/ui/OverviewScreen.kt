package dev.code04.lastro.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.code04.lastro.R
import dev.code04.lastro.data.AppRepository
import dev.code04.lastro.data.ProblemKind
import dev.code04.lastro.data.Saved
import dev.code04.lastro.history.CollectionSummary
import dev.code04.lastro.net.Comparison
import dev.code04.lastro.net.ExpenseQuery
import dev.code04.lastro.net.Overview
import java.time.LocalDate
import java.time.YearMonth

data class CollectionGlance(val title: String, val chip: String, val tone: Tone, val summary: CollectionSummary?)

@Composable
fun OverviewScreen(
    repository: AppRepository,
    snapshot: StatusSnapshot,
    glance: CollectionGlance,
    refreshKey: Int,
    onOpenList: (ExpenseQuery) -> Unit,
    onOpenCollection: () -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var month by rememberSaveable { mutableStateOf<String?>(null) }
    val loaded = load(month, refreshKey, snapshot.hasDestination) { repository.overview(month) }
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(start = 16.dp, end = 16.dp, top = 4.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        if (!snapshot.hasDestination) {
            FirstUse(snapshot, onOpenSettings)
            return@Column
        }
        when (val state = loaded.state) {
            Load.Loading -> OverviewSkeleton(month)
            is Load.Failed -> {
                val saved = if (state.problem.kind == ProblemKind.NETWORK && month == null) repository.savedOverview() else null
                if (saved != null) {
                    OfflineNotice(saved, loaded.retry)
                    OverviewContent(saved.value, month, { month = it }, glance, onOpenList, onOpenCollection, onOpenSettings, stale = true)
                } else {
                    PeriodPicker(month ?: YearMonth.now().toString(), inProgress = month == null) { month = it }
                    ProblemCard(state.problem, loaded.retry, onOpenSettings)
                }
            }
            is Load.Ready -> OverviewContent(state.value, month, { month = it }, glance, onOpenList, onOpenCollection, onOpenSettings, stale = false)
        }
    }
}

@Composable
private fun OverviewContent(
    overview: Overview,
    month: String?,
    onPickMonth: (String?) -> Unit,
    glance: CollectionGlance,
    onOpenList: (ExpenseQuery) -> Unit,
    onOpenCollection: () -> Unit,
    onOpenSettings: () -> Unit,
    stale: Boolean,
) {
    val period = overview.period
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        PeriodPicker(period.month, period.inProgress) { picked -> onPickMonth(picked.takeIf { it != YearMonth.now().toString() }) }
        val moment = overview.generatedAt?.let { if (stale) "Atualizado em ${formatMoment(it)}" else "Atualizado hoje às ${formatClock(it)}" } ?: "Atualização desconhecida"
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Dot(if (stale) MaterialTheme.colorScheme.outline else if (overview.coverage.complete) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.tertiary)
            Text("$moment · por data da compra · ${overview.currency}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
    CoverageNotice(overview, onOpenCollection, onOpenSettings)
    SummaryCard(overview) { onOpenList(ExpenseQuery(month = period.month, origin = "Visão geral")) }
    if (overview.categories.isNotEmpty()) CategoriesCard(overview, onOpenList)
    if (overview.totals.gross.count > 0 || overview.days.any { it.coverage != "complete" && it.coverage != "future" }) DailyCard(overview) {
        onOpenList(ExpenseQuery(month = period.month, origin = "Visão geral › Evolução diária"))
    }
    if (month == null) CollectionGlanceCard(glance, onOpenCollection)
}

@Composable
private fun PeriodPicker(month: String, inProgress: Boolean, onPick: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Box {
            OutlinedButton(onClick = { open = true }, modifier = Modifier.heightIn(min = 44.dp).semantics { contentDescription = "Período: ${Format.monthTitle(month)}. Trocar período" }) {
                AppIcon(R.drawable.ic_calendar, tint = MaterialTheme.colorScheme.onSurface, size = 18.dp)
                Text(" ${Format.monthTitle(month)} ", style = MaterialTheme.typography.titleSmall)
                AppIcon(R.drawable.ic_chevron_down, tint = MaterialTheme.colorScheme.onSurface, size = 18.dp)
            }
            DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
                val current = YearMonth.now()
                (0 until 12).map { current.minusMonths(it.toLong()).toString() }.forEach { option ->
                    DropdownMenuItem(text = { Text(Format.monthTitle(option)) }, onClick = {
                        open = false
                        onPick(option)
                    })
                }
            }
        }
        Text(
            if (inProgress) "mês em andamento" else "mês fechado",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun CoverageNotice(overview: Overview, onOpenCollection: () -> Unit, onOpenSettings: () -> Unit) {
    val coverage = overview.coverage
    if (coverage.complete || coverage.limitations.isEmpty()) return
    val gaps = overview.days.any { it.coverage == "unknown" || it.coverage == "partial" }
    val paused = coverage.trackingState == "paused"
    val title = when {
        paused -> "Coleta pausada" + (coverage.trackingSince?.let { " desde ${formatMoment(it)}" } ?: "")
        coverage.trackingState == "never_enabled" -> "O acompanhamento automático não está ativo"
        gaps -> "Este período pode estar incompleto"
        else -> "Ainda há notificações para a Case ler"
    }
    Notice(
        text = coverage.limitations.take(3).joinToString(" "),
        tone = if (paused || gaps || coverage.trackingState == "never_enabled") Tone.WAITING else Tone.NEUTRAL,
        icon = if (paused) R.drawable.ic_pause else R.drawable.ic_info,
        title = title,
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (paused) TonalButton("Abrir Configurações", onOpenSettings)
            LinkButton("Ver coleta", onOpenCollection)
        }
    }
}

@Composable
private fun SummaryCard(overview: Overview, onOpenList: () -> Unit) {
    val hidden = LocalHidden.current
    val totals = overview.totals
    SectionCard(title = null) {
        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(
                "Total bruto registrado" + if (!overview.coverage.complete && totals.gross.count > 0) " · pode estar incompleto" else "",
                style = MaterialTheme.typography.labelLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (totals.gross.count == 0) {
                Text("Nenhuma despesa registrada neste recorte", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
                if (!overview.coverage.complete) {
                    Text(
                        "Isso não quer dizer que não houve gastos: a cobertura deste período é desconhecida ou parcial.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.tertiary,
                    )
                }
            } else {
                Text(
                    Format.money(totals.gross.amount, hidden),
                    style = MaterialTheme.typography.displaySmall,
                    fontWeight = FontWeight.SemiBold,
                    color = if (hidden) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.onSurface,
                )
                val average = totals.average?.takeIf { !hidden }?.let { " · valor médio ${it.formatted}" }.orEmpty()
                Text(
                    Format.count(totals.gross.count, "despesa confirmada", "despesas confirmadas") + average,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (totals.gross.count > 0) ComparisonLine(overview.comparison, overview.period.inProgress)
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Column(Modifier.weight(1f)) {
                Text("Estornos", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text(Format.money(totals.refunds.amount, hidden), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                Text(
                    if (totals.refunds.count == 0) "nenhum no período" else Format.count(totals.refunds.count, "estorno", "estornos") + ", pela data do estorno",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Column(Modifier.weight(1f)) {
                Text("Total líquido", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text(Format.money(totals.net, hidden), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                Text(
                    if (totals.net.minor < 0) "estornos superaram as despesas registradas" else "bruto menos estornos",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        val outside = buildList {
            if (totals.provisional.count > 0) add(Format.count(totals.provisional.count, "despesa provisória", "despesas provisórias") to Format.money(totals.provisional.amount, hidden))
            if (totals.pending.count > 0) add(Format.count(totals.pending.count, "despesa em conferência", "despesas em conferência") to Format.money(totals.pending.amount, hidden))
            if (totals.unknownTotal > 0) add(Format.count(totals.unknownTotal, "compra sem valor integral conhecido", "compras sem valor integral conhecido") to "indisponível")
        }
        if (outside.isNotEmpty()) {
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            Overline("Fora do total")
            outside.forEach { (label, value) -> InfoRow(label, value, MaterialTheme.colorScheme.onSurface) }
        }
        val listed = totals.gross.count + totals.provisional.count + totals.pending.count + totals.unknownTotal
        if (listed > 0) LinkButton("Ver as ${Format.count(totals.gross.count, "despesa", "despesas")}" + if (listed > totals.gross.count) " e o que está fora do total" else "", onOpenList)
    }
}

@Composable
private fun ComparisonLine(comparison: Comparison, inProgress: Boolean) {
    val hidden = LocalHidden.current
    val previousMonth = Format.monthName(comparison.previousFrom.take(7))
    val range = if (inProgress) "de ${Format.dayRange(comparison.previousFrom, comparison.previousTo)}" else "de $previousMonth"
    val difference = comparison.difference
    val text = when {
        !comparison.available && comparison.reason == "no_history" -> "Comparação com $previousMonth indisponível: o Lastro ainda não acompanhava esse período."
        !comparison.available -> "Comparação com $previousMonth indisponível enquanto houver lacuna em um dos períodos."
        difference == null || difference.minor == 0L -> "Igual ao registrado $range."
        else -> {
            val direction = if (difference.minor > 0) "a mais" else "a menos"
            val percent = comparison.percentBasisPoints?.let { " (${Format.signedPercent(it)})" }
            when {
                hidden && percent != null -> "${Format.signedPercent(comparison.percentBasisPoints)} em relação ao registrado $range."
                hidden -> "Valor $direction que o registrado $range; sem base para variação percentual."
                percent == null -> "${Format.magnitude(difference, false)} $direction que $range; sem base para variação percentual."
                else -> "${Format.magnitude(difference, false)} $direction$percent que $range" + if (inProgress) ", nos mesmos dias." else "."
            }
        }
    }
    Row(
        Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(12.dp)).padding(12.dp),
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        AppIcon(if (comparison.available) R.drawable.ic_trend else R.drawable.ic_minus, size = 18.dp)
        Text(text, style = MaterialTheme.typography.bodyMedium)
    }
}

@Composable
private fun CategoriesCard(overview: Overview, onOpenList: (ExpenseQuery) -> Unit) {
    val hidden = LocalHidden.current
    val groups = overview.categories
    val max = groups.maxOfOrNull { it.gross.amount.minor }?.takeIf { it > 0 } ?: 1L
    val subtitle = if (hidden) "Despesas confirmadas · participação no total" else "Despesas confirmadas · ${overview.totals.gross.amount.formatted} = 100%"
    SectionCard(title = "Por categoria", subtitle = subtitle) {
        Column {
            val concluded = groups.filter { it.kind == "category" || it.kind == "other" }
            val pending = groups.filter { it.kind == "provisional_classification" || it.kind == "uncategorized" }
            concluded.forEach { group ->
                BarRow(
                    label = group.label,
                    detail = if (group.kind == "other") Format.count(group.categories, "categoria", "categorias") else null,
                    value = Format.money(group.gross.amount, hidden),
                    share = group.shareBasisPoints?.let(Format::percent),
                    fraction = group.gross.amount.minor.toFloat() / max,
                    style = if (group.kind == "other") BarStyle.MUTED else BarStyle.SOLID,
                    onClick = { onOpenList(ExpenseQuery.ofGroup(group, "Visão geral › Por categoria")) },
                )
            }
            if (pending.isNotEmpty()) {
                HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                Overline("Classificação não concluída")
                pending.forEach { group ->
                    BarRow(
                        label = group.label,
                        value = Format.money(group.gross.amount, hidden),
                        share = group.shareBasisPoints?.let(Format::percent),
                        fraction = group.gross.amount.minor.toFloat() / max,
                        style = if (group.kind == "uncategorized") BarStyle.DASHED else BarStyle.STRIPED,
                        onClick = { onOpenList(ExpenseQuery.ofGroup(group, "Visão geral › Por categoria")) },
                    )
                }
            }
        }
        Text(
            "Percentuais arredondados; a soma pode não dar exatamente 100%. Provisórias e sem categoria ficam fora de “Outras”.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun DailyCard(overview: Overview, onOpenList: () -> Unit) {
    val hidden = LocalHidden.current
    val days = overview.days
    val observed = days.filter { it.coverage != "future" }
    var selected by rememberSaveable(overview.period.month) {
        mutableStateOf(observed.lastOrNull { it.gross.count > 0 }?.date ?: observed.lastOrNull()?.date ?: days.firstOrNull()?.date)
    }
    SectionCard(title = "Evolução diária", subtitle = "Total bruto por dia da compra · despesas confirmadas") {
        val index = observed.indexOfFirst { it.date == selected }
        val day = observed.getOrNull(index)
        if (day != null) {
            Row(
                Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(12.dp)),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                IconButton(onClick = { observed.getOrNull(index - 1)?.let { selected = it.date } }, enabled = index > 0) {
                    AppIcon(R.drawable.ic_back, size = 18.dp)
                }
                Column(Modifier.weight(1f).padding(vertical = 8.dp)) {
                    Text(Format.dayHeading(day.date, LocalDate.now()), style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
                    Text(
                        if (day.gross.count == 0) "nenhuma despesa registrada" else "${Format.money(day.gross.amount, hidden)} · ${Format.count(day.gross.count, "despesa", "despesas")}",
                        style = MaterialTheme.typography.bodyMedium,
                    )
                    when (day.coverage) {
                        "unknown" -> Text("cobertura desconhecida: pode haver mais compras", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.tertiary)
                        "partial" -> Text("cobertura parcial: pode haver mais compras", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.tertiary)
                    }
                }
                IconButton(onClick = { observed.getOrNull(index + 1)?.let { selected = it.date } }, enabled = index < observed.lastIndex) {
                    AppIcon(R.drawable.ic_chevron_right, size = 18.dp)
                }
            }
        }
        val peak = observed.maxByOrNull { it.gross.amount.minor }
        val description = buildString {
            append("Colunas com o total bruto de cada dia de ${Format.dayRange(overview.period.from, overview.period.to)}.")
            if (peak != null && peak.gross.count > 0 && !hidden) append(" Maior valor em ${Format.shortDate(peak.date)}: ${peak.gross.amount.formatted}.")
        }
        DailyColumns(days, selected, { selected = it }, description)
        val legend = buildList<Pair<String, @Composable () -> Unit>> {
            add("com despesas" to { SolidSwatch() })
            if (observed.any { it.gross.count == 0 && it.coverage == "complete" }) add("sem despesas registradas" to { LineSwatch() })
            if (observed.any { it.coverage == "unknown" || it.coverage == "partial" }) add("cobertura desconhecida" to { StripedSwatch() })
            if (days.any { it.coverage == "future" }) add("ainda não aconteceu" to { LineSwatch(MaterialTheme.colorScheme.outline) })
        }
        ChartLegend(legend)
        val refundDays = days.filter { (it.refunds?.count ?: 0) > 0 }
        if (refundDays.isNotEmpty()) {
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            Text(
                "Estornos ficam fora das colunas: ${Format.money(overview.totals.refunds.amount, hidden)} em " + refundDays.joinToString(", ") { Format.shortDate(it.date) } + ".",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        LinkButton("Ver valores por dia", onOpenList)
    }
}

@Composable
fun CollectionGlanceCard(glance: CollectionGlance, onOpen: () -> Unit) {
    val summary = glance.summary
    Row(
        Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.surfaceContainerLow, RoundedCornerShape(20.dp))
            .outlined(MaterialTheme.colorScheme.outlineVariant, 20.dp)
            .tappable("Abrir coleta", onOpen)
            .padding(16.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        IconCircle(R.drawable.ic_tab_collection, glance.tone)
        Column(Modifier.weight(1f)) {
            Text(glance.title, style = MaterialTheme.typography.titleSmall)
            if (summary != null) {
                Text(
                    listOf(
                        "${summary.awaitingSend} aguardando envio",
                        "${summary.awaitingResult} aguardando resultado",
                        "${summary.blocked} com impedimento",
                    ).joinToString(" · "),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        ChevronIcon()
    }
}

@Composable
private fun OfflineNotice(saved: Saved<Overview>, onRetry: () -> Unit) {
    Notice(
        text = "Você está vendo a última visão salva, de ${formatTime(saved.savedAt)}. Despesas registradas depois disso ainda não aparecem.",
        tone = Tone.NEUTRAL,
        icon = R.drawable.ic_wifi_off,
        title = "Sem conexão com o Lastro",
    ) { TonalButton("Tentar de novo", onRetry) }
}

@Composable
private fun OverviewSkeleton(month: String?) {
    PeriodPicker(month ?: YearMonth.now().toString(), inProgress = month == null) { }
    Text(
        "Consultando ${Format.monthName(month ?: YearMonth.now().toString())}… os totais aparecem quando a consulta terminar.",
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    SectionCard(title = null) {
        Text("Total bruto registrado", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Skeleton(190.dp, 40.dp)
        Skeleton(230.dp, 14.dp)
        Skeleton(null, 44.dp)
    }
    SectionCard(title = "Por categoria") {
        repeat(4) {
            Skeleton(140.dp, 12.dp)
            Skeleton(null, 10.dp)
        }
    }
}

@Composable
private fun FirstUse(snapshot: StatusSnapshot, onOpenSettings: () -> Unit) {
    Column(Modifier.padding(top = 8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("Aqui você consulta o que já está registrado.", style = MaterialTheme.typography.headlineSmall)
        Text(
            "Despesas por período, por categoria e por dia, e como anda a coleta das notificações. Registrar, corrigir e classificar continua na conversa com a Case.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
    SectionCard(title = "Para começar") {
        StepRow(1, "Permitir acesso às notificações", "Só as do Google Wallet. Números de cartão e códigos são escondidos antes de guardar.", snapshot.notificationAccess)
        StepRow(2, "Liberar da economia de bateria", "Assim o Android não interrompe a coleta com a tela desligada.", snapshot.batteryUnrestricted)
        StepRow(3, "Conectar ao Lastro", "Informe o servidor e a credencial que permite enviar notificações e consultar despesas.", snapshot.hasDestination)
        PrimaryButton("Abrir Configurações", onOpenSettings)
    }
    Notice(
        "A coleta vale a partir da ativação: compras anteriores não chegam pelo celular. Os totais aparecem aqui assim que houver despesas registradas.",
        Tone.NEUTRAL,
    )
}

@Composable
private fun StepRow(number: Int, title: String, description: String, done: Boolean) {
    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Box(
            Modifier.padding(top = 2.dp).background(
                if (done) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surfaceContainerHighest,
                RoundedCornerShape(50),
            ).padding(horizontal = 9.dp, vertical = 3.dp),
        ) {
            Text(
                if (done) "✓" else number.toString(),
                color = if (done) MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurface,
                style = MaterialTheme.typography.labelLarge,
            )
        }
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleSmall)
            Text(if (done) "feito" else description, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
