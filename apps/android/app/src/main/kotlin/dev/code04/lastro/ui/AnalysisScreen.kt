package dev.code04.lastro.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.code04.lastro.R
import dev.code04.lastro.data.AppRepository
import dev.code04.lastro.net.Analysis
import dev.code04.lastro.net.CategoryChange
import dev.code04.lastro.net.ExpenseQuery

@Composable
fun AnalysisScreen(
    repository: AppRepository,
    refreshKey: Int,
    onOpenList: (ExpenseQuery) -> Unit,
    onOpenOverview: () -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val loaded = load(refreshKey) { repository.analysis(6) }
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(start = 16.dp, end = 16.dp, top = 4.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text("Despesas confirmadas · por data da compra · BRL", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        when (val state = loaded.state) {
            Load.Loading -> {
                SectionCard(title = "Total bruto por mês") { Skeleton(null, 172.dp) }
                SectionCard(title = "Mesmos dias, mês a mês") { Skeleton(null, 60.dp) }
            }
            is Load.Failed -> ProblemCard(state.problem, loaded.retry, onOpenSettings)
            is Load.Ready -> AnalysisContent(state.value, onOpenList, onOpenOverview)
        }
    }
}

@Composable
private fun AnalysisContent(analysis: Analysis, onOpenList: (ExpenseQuery) -> Unit, onOpenOverview: () -> Unit) {
    val hidden = LocalHidden.current
    val closedWithData = analysis.months.filter { !it.inProgress && it.coverage != "none" }
    MonthsCard(analysis, hidden, onOpenList)
    if (closedWithData.isEmpty() && !analysis.sameDays.available) {
        SectionCard(title = null) {
            IconCircle(R.drawable.ic_calendar, Tone.NEUTRAL, 44.dp)
            Text("Ainda não há histórico para comparar", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(
                analysis.historyStartsOn?.let {
                    "O Lastro acompanha suas compras desde ${Format.fullDate(it)}. A comparação dos mesmos dias fica disponível quando houver um mês anterior acompanhado."
                } ?: "O acompanhamento automático ainda não foi ativado. A comparação fica disponível quando houver um mês anterior acompanhado.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            InfoRow("Mesmos dias, mês a mês", "indisponível")
            InfoRow("Variação por categoria", "indisponível")
            LinkButton("Ver o mês atual em Visão geral", onOpenOverview)
        }
        return
    }
    SameDaysCard(analysis, hidden)
    CategoriesComparisonCard(analysis, hidden)
}

@Composable
private fun MonthsCard(analysis: Analysis, hidden: Boolean, onOpenList: (ExpenseQuery) -> Unit) {
    var table by rememberSaveable { mutableStateOf(false) }
    val months = analysis.months
    SectionCard(
        title = "Total bruto por mês",
        subtitle = months.firstOrNull()?.let { "${Format.monthTitle(it.month)} a ${Format.monthTitle(months.last().month)}" },
    ) {
        MonthColumns(months, hidden)
        ChartLegend(
            buildList {
                add("mês fechado" to { SolidSwatch() })
                add("mês em andamento" to { InProgressSwatch() })
                if (months.any { it.coverage == "none" && it.gross.count == 0 }) add("sem dados no Lastro" to { OutlineSwatch(MaterialTheme.colorScheme.outline) })
            },
        )
        if (months.lastOrNull()?.inProgress == true) {
            Text(
                "${Format.monthName(months.last().month).replaceFirstChar(Char::uppercase)} ainda não terminou e não é comparado com meses inteiros. Para comparar, use os mesmos dias abaixo.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        if (months.any { it.coverage == "partial" }) {
            Text(
                "Meses com cobertura parcial podem ter compras que não chegaram ao Lastro.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.tertiary,
            )
        }
        LinkButton(if (table) "Esconder tabela" else "Ver valores em tabela", { table = !table })
        if (table) {
            Column(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(12.dp)).padding(12.dp)) {
                months.forEach { month ->
                    Row(Modifier.fillMaxWidth().tappable("Ver despesas de ${Format.monthTitle(month.month)}") { onOpenList(ExpenseQuery(month = month.month, origin = "Análises")) }.padding(vertical = 8.dp)) {
                        Text(
                            Format.monthTitle(month.month) + if (month.inProgress) " (até ${Format.shortDate(month.to)})" else "",
                            Modifier.weight(1f),
                            style = MaterialTheme.typography.bodyMedium,
                        )
                        Text(
                            when {
                                month.coverage == "none" && month.gross.count == 0 -> "sem dados"
                                else -> "${Format.money(month.gross.amount, hidden)} · ${month.gross.count}"
                            },
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun SameDaysCard(analysis: Analysis, hidden: Boolean) {
    val comparison = analysis.sameDays
    val currentMonth = Format.monthName(comparison.currentFrom.take(7)).replaceFirstChar(Char::uppercase)
    val previousMonth = Format.monthName(comparison.previousFrom.take(7)).replaceFirstChar(Char::uppercase)
    SectionCard(title = "Mesmos dias, mês a mês", subtitle = "Dias ${Format.dayRange(comparison.currentFrom, comparison.currentTo).substringBefore(" de ")} · corte no último dia observado") {
        if (!comparison.available) {
            Text(
                if (comparison.reason == "no_history") "Comparação indisponível: o Lastro ainda não acompanhava $previousMonth." else "Comparação indisponível: há uma lacuna de cobertura em um dos períodos.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            return@SectionCard
        }
        val max = maxOf(comparison.current.minor, comparison.previous.minor).coerceAtLeast(1)
        Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row { Text("$currentMonth, ${Format.dayRange(comparison.currentFrom, comparison.currentTo).substringBefore(" de ")}", Modifier.weight(1f), fontWeight = FontWeight.Medium); Text(Format.money(comparison.current, hidden), fontWeight = FontWeight.SemiBold) }
            Bar(comparison.current.minor.toFloat() / max, BarStyle.SOLID)
        }
        Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row { Text("$previousMonth, ${Format.dayRange(comparison.previousFrom, comparison.previousTo).substringBefore(" de ")}", Modifier.weight(1f), fontWeight = FontWeight.Medium); Text(Format.money(comparison.previous, hidden), fontWeight = FontWeight.SemiBold) }
            Bar(comparison.previous.minor.toFloat() / max, BarStyle.OUTLINE)
        }
        val difference = comparison.difference
        if (difference != null) {
            val direction = if (difference.minor >= 0) "a mais" else "a menos"
            val text = when {
                difference.minor == 0L -> "Igual nos mesmos dias."
                comparison.percentBasisPoints == null -> "${Format.magnitude(difference, hidden)} $direction; sem base para variação percentual."
                hidden -> "${Format.signedPercent(comparison.percentBasisPoints)} em ${currentMonth.lowercase()}, nos mesmos dias."
                else -> "${Format.magnitude(difference, false)} $direction (${Format.signedPercent(comparison.percentBasisPoints)}) em ${currentMonth.lowercase()}, nos mesmos dias."
            }
            Row(
                Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(12.dp)).padding(12.dp),
                horizontalArrangement = Arrangement.spacedBy(10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                AppIcon(R.drawable.ic_trend, size = 18.dp)
                Text(text, style = MaterialTheme.typography.bodyMedium)
            }
        }
    }
}

@Composable
private fun CategoriesComparisonCard(analysis: Analysis, hidden: Boolean) {
    if (!analysis.sameDays.available) return
    var all by rememberSaveable { mutableStateOf(false) }
    val changes = analysis.categories
    val shown = if (all) changes else changes.take(5)
    val previousMonth = Format.monthName(analysis.sameDays.previousFrom.take(7))
    val currentMonth = Format.monthName(analysis.sameDays.currentFrom.take(7))
    SectionCard(title = "Por categoria", subtitle = "${currentMonth.replaceFirstChar(Char::uppercase)} e $previousMonth, mesmos dias") {
        ChartLegend(listOf(currentMonth to { SolidSwatch() }, previousMonth to { OutlineSwatch() }))
        val max = changes.maxOfOrNull { maxOf(it.current.minor, it.previous.minor) }?.coerceAtLeast(1) ?: 1L
        shown.forEachIndexed { index, change ->
            if (index > 0) HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            ChangeRow(change, max, hidden, previousMonth)
        }
        if (analysis.provisional + analysis.uncategorized > 0) {
            Notice(
                "Em $currentMonth, ${Format.count(analysis.provisional, "despesa ainda tem", "despesas ainda têm")} classificação provisória e ${Format.count(analysis.uncategorized, "está", "estão")} sem categoria. As variações por categoria podem mudar quando forem concluídas.",
                Tone.WAITING,
            )
        }
        if (changes.size > 5) LinkButton(if (all) "Mostrar só as maiores" else "Ver ranking completo (${changes.size} categorias)", { all = !all })
    }
}

@Composable
private fun ChangeRow(change: CategoryChange, max: Long, hidden: Boolean, previousMonth: String) {
    Column(Modifier.padding(vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(5.dp)) {
        Row {
            Text(change.label, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Medium)
            Text(
                when {
                    change.difference.minor == 0L -> "sem variação"
                    hidden -> if (change.difference.minor > 0) "aumentou" else "diminuiu"
                    change.difference.minor > 0 -> "+" + change.difference.formatted
                    else -> "−" + change.difference.formatted.removePrefix("-")
                },
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.SemiBold,
                color = if (change.difference.minor == 0L) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.onSurface,
            )
        }
        Bar(change.current.minor.toFloat() / max, BarStyle.SOLID)
        Bar(change.previous.minor.toFloat() / max, BarStyle.OUTLINE)
        Text(
            "${Format.money(change.current, hidden)} · $previousMonth ${Format.money(change.previous, hidden)}",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}
