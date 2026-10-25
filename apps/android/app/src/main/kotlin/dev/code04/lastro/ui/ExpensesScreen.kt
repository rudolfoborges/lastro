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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import dev.code04.lastro.R
import dev.code04.lastro.data.AppRepository
import dev.code04.lastro.data.Fetch
import dev.code04.lastro.data.Problem
import dev.code04.lastro.net.CategoryGroup
import dev.code04.lastro.net.ExpenseItem
import dev.code04.lastro.net.ExpenseList
import dev.code04.lastro.net.ExpenseQuery
import dev.code04.lastro.net.Tally
import kotlinx.coroutines.launch
import java.time.LocalDate
import java.time.YearMonth

@Composable
fun ExpensesScreen(
    repository: AppRepository,
    initial: ExpenseQuery,
    refreshKey: Int,
    onOpenExpense: (String) -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var query by remember(initial) { mutableStateOf(initial) }
    var text by rememberSaveable(initial) { mutableStateOf(initial.text) }
    var first by remember(query, refreshKey) { mutableStateOf<Load<ExpenseList>>(Load.Loading) }
    val more = remember(query, refreshKey) { mutableStateListOf<ExpenseItem>() }
    var nextOffset by remember(query, refreshKey) { mutableStateOf<Int?>(null) }
    var attempt by remember { mutableIntStateOf(0) }
    var loadingMore by remember(query, refreshKey) { mutableStateOf(false) }
    var moreProblem by remember(query, refreshKey) { mutableStateOf<Problem?>(null) }
    val scope = rememberCoroutineScope()
    LaunchedEffect(query, refreshKey, attempt) {
        when (val fetch = repository.expenses(query, 0)) {
            is Fetch.Ok -> {
                first = Load.Ready(fetch.value)
                nextOffset = fetch.value.nextOffset
            }
            is Fetch.Failed -> first = Load.Failed(fetch.problem)
        }
    }
    val groups = load(query.month ?: query.from?.take(7), refreshKey) { repository.overview(query.month ?: query.from?.take(7)) }
    LazyColumn(
        modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 4.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        if (query.origin != null) {
            item { Text("Aberta a partir de ${query.origin}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
        item {
            OutlinedTextField(
                value = text,
                onValueChange = { text = it },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true,
                placeholder = { Text("Buscar por descrição") },
                leadingIcon = { AppIcon(R.drawable.ic_search) },
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(onSearch = { query = query.copy(text = text) }),
                shape = RoundedCornerShape(24.dp),
            )
        }
        item {
            Filters(query, (groups.state as? Load.Ready)?.value?.categories.orEmpty()) { query = it }
        }
        when (val state = first) {
            Load.Loading -> item { ListSkeleton() }
            is Load.Failed -> item { ProblemCard(state.problem, { attempt++ }, onOpenSettings) }
            is Load.Ready -> {
                val list = state.value
                if (list.matching > 0) item { SliceSummary(list, query) { query = query.cleared().copy(origin = null); text = "" } }
                if (list.items.isEmpty()) {
                    item { EmptyList(query) { query = query.cleared().copy(origin = null); text = "" } }
                }
                val items = list.items + more
                items.groupBy { it.date }.forEach { (date, dayItems) ->
                    item(key = "day-$date") { DayHeader(date, list.days[date]) }
                    item(key = "items-$date") { DayItems(dayItems, onOpenExpense) }
                }
                if (nextOffset != null) {
                    item {
                        Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.fillMaxWidth()) {
                            moreProblem?.let { Text(it.message, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error) }
                            TonalButton(if (loadingMore) "Carregando…" else "Carregar mais", {
                                val offset = nextOffset ?: return@TonalButton
                                if (loadingMore) return@TonalButton
                                loadingMore = true
                                scope.launch {
                                    when (val fetch = repository.expenses(query, offset)) {
                                        is Fetch.Ok -> {
                                            more.addAll(fetch.value.items)
                                            nextOffset = fetch.value.nextOffset
                                            moreProblem = null
                                        }
                                        is Fetch.Failed -> moreProblem = fetch.problem
                                    }
                                    loadingMore = false
                                }
                            })
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun Filters(query: ExpenseQuery, groups: List<CategoryGroup>, onChange: (ExpenseQuery) -> Unit) {
    var openMonth by remember { mutableStateOf(false) }
    var openCategory by remember { mutableStateOf(false) }
    var openClassification by remember { mutableStateOf(false) }
    var openStatus by remember { mutableStateOf(false) }
    val month = query.month ?: query.from?.take(7) ?: YearMonth.now().toString()
    LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        item {
            Box {
                FilterChip(
                    selected = true,
                    onClick = { openMonth = true },
                    label = { Text(Format.monthTitle(month)) },
                    trailingIcon = { AppIcon(R.drawable.ic_chevron_down, size = 16.dp) },
                )
                DropdownMenu(expanded = openMonth, onDismissRequest = { openMonth = false }) {
                    val current = YearMonth.now()
                    (0 until 12).map { current.minusMonths(it.toLong()).toString() }.forEach { option ->
                        DropdownMenuItem(text = { Text(Format.monthTitle(option)) }, onClick = {
                            openMonth = false
                            onChange(query.copy(month = option, from = null, to = null))
                        })
                    }
                }
            }
        }
        item {
            Box {
                val label = query.label ?: if (query.categories.isNotEmpty()) query.categories.joinToString() else "Categoria"
                FilterChip(
                    selected = query.label != null || query.categories.isNotEmpty(),
                    onClick = { openCategory = true },
                    label = { Text(label) },
                    trailingIcon = { AppIcon(R.drawable.ic_chevron_down, size = 16.dp) },
                )
                DropdownMenu(expanded = openCategory, onDismissRequest = { openCategory = false }) {
                    DropdownMenuItem(text = { Text("Todas as categorias") }, onClick = {
                        openCategory = false
                        onChange(query.copy(categories = emptyList(), classifications = emptyList(), statuses = emptyList(), natures = emptyList(), label = null))
                    })
                    groups.forEach { group ->
                        DropdownMenuItem(text = { Text(group.label) }, onClick = {
                            openCategory = false
                            onChange(ExpenseQuery.ofGroup(group, "Despesas").copy(month = query.month, text = query.text, origin = query.origin))
                        })
                    }
                }
            }
        }
        item {
            Box {
                val label = when (query.classifications.singleOrNull()) {
                    "confirmed" -> "Classificação concluída"
                    "suggested" -> "Classificação provisória"
                    "missing" -> "Classificação pendente"
                    else -> "Classificação"
                }
                FilterChip(
                    selected = query.classifications.isNotEmpty(),
                    onClick = { openClassification = true },
                    label = { Text(label) },
                    trailingIcon = { AppIcon(R.drawable.ic_chevron_down, size = 16.dp) },
                )
                DropdownMenu(expanded = openClassification, onDismissRequest = { openClassification = false }) {
                    listOf(null to "Todas", "confirmed" to "Concluída", "suggested" to "Provisória", "missing" to "Pendente").forEach { (value, title) ->
                        DropdownMenuItem(text = { Text(title) }, onClick = {
                            openClassification = false
                            onChange(query.copy(classifications = listOfNotNull(value), label = query.label.takeIf { query.categories.isNotEmpty() }))
                        })
                    }
                }
            }
        }
        item {
            Box {
                val label = when (query.statuses.singleOrNull()) {
                    "confirmed" -> "Confirmadas"
                    "provisional" -> "Provisórias"
                    "pending_clarification" -> "Em conferência"
                    else -> "Situação"
                }
                FilterChip(
                    selected = query.statuses.isNotEmpty(),
                    onClick = { openStatus = true },
                    label = { Text(label) },
                    trailingIcon = { AppIcon(R.drawable.ic_chevron_down, size = 16.dp) },
                )
                DropdownMenu(expanded = openStatus, onDismissRequest = { openStatus = false }) {
                    listOf(null to "Todas", "confirmed" to "Confirmadas", "provisional" to "Provisórias", "pending_clarification" to "Em conferência").forEach { (value, title) ->
                        DropdownMenuItem(text = { Text(title) }, onClick = {
                            openStatus = false
                            onChange(query.copy(statuses = listOfNotNull(value)))
                        })
                    }
                }
            }
        }
        item { FilterChip(selected = false, onClick = { }, enabled = false, label = { Text(query.currency) }) }
    }
}

@Composable
private fun SliceSummary(list: ExpenseList, query: ExpenseQuery, onClear: () -> Unit) {
    val hidden = LocalHidden.current
    val totals = list.totals
    SectionCard(title = null) {
        Row(verticalAlignment = Alignment.Bottom) {
            Text(
                Format.count(totals.gross.count, "despesa confirmada", "despesas confirmadas"),
                Modifier.weight(1f),
                style = MaterialTheme.typography.titleSmall,
            )
            Text(Format.money(totals.gross.amount, hidden), style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
        }
        val origin = if (query.origin?.contains("Por categoria") == true) " Igual à barra em Visão geral." else ""
        Text(
            "Total bruto de ${Format.dayRange(list.period.from, list.period.to)}, por data da compra.$origin",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        val outside = buildList {
            if (totals.provisional.count > 0) add("${Format.count(totals.provisional.count, "despesa provisória", "despesas provisórias")} (${Format.money(totals.provisional.amount, hidden)})")
            if (totals.pending.count > 0) add("${Format.count(totals.pending.count, "em conferência", "em conferência")} (${Format.money(totals.pending.amount, hidden)})")
            if (totals.unknownTotal > 0) add(Format.count(totals.unknownTotal, "compra sem valor integral conhecido", "compras sem valor integral conhecido"))
            if (totals.refunds.count > 0) add("${Format.count(totals.refunds.count, "estorno", "estornos")} (${Format.money(totals.refunds.amount, hidden)})")
        }
        if (outside.isNotEmpty()) {
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            Text("Também na lista, fora do total: ${outside.joinToString(", ")}.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                "${Format.count(list.matching, "item", "itens")} · mais recentes primeiro",
                Modifier.weight(1f),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (query.filtered || query.label != null) LinkButton("Limpar filtros", onClear)
        }
    }
}

@Composable
private fun EmptyList(query: ExpenseQuery, onClear: () -> Unit) {
    val filtered = query.filtered || query.label != null
    Column(
        Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainer, RoundedCornerShape(24.dp)).padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        IconCircle(if (filtered) R.drawable.ic_filter else R.drawable.ic_tab_expenses, Tone.NEUTRAL, 56.dp)
        Text(if (filtered) "Nenhuma despesa com estes filtros" else "Nenhuma despesa registrada neste recorte", style = MaterialTheme.typography.titleMedium)
        Text(
            if (filtered) "Os filtros atuais não encontram itens. O período pode ter outras despesas." else "Isso não quer dizer que não houve gastos: confira a cobertura em Visão geral.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (filtered) PrimaryButton("Limpar filtros", onClear)
    }
}

@Composable
private fun DayHeader(date: String, total: Tally?) {
    val hidden = LocalHidden.current
    Row(Modifier.fillMaxWidth().padding(top = 4.dp, start = 4.dp, end = 4.dp), verticalAlignment = Alignment.Bottom) {
        Text(Format.dayHeading(date, LocalDate.now()), Modifier.weight(1f), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (total != null && total.count > 0) Text(Format.money(total.amount, hidden), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun DayItems(items: List<ExpenseItem>, onOpen: (String) -> Unit) {
    Column(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainer, RoundedCornerShape(16.dp))) {
        items.forEachIndexed { index, item ->
            if (index > 0) HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            ExpenseRow(item) { onOpen(item.id) }
        }
    }
}

@Composable
fun ExpenseRow(item: ExpenseItem, onClick: () -> Unit) {
    val hidden = LocalHidden.current
    Row(
        Modifier.fillMaxWidth().heightIn(min = 64.dp).tappable("Ver despesa", onClick).padding(horizontal = 16.dp, vertical = 12.dp).semantics(mergeDescendants = true) {},
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(item.merchant ?: item.description, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium)
            androidx.compose.foundation.layout.FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                if (item.nature == "refund") Tag("estorno", Tone.NEUTRAL)
                if (item.status == "provisional") Tag("despesa provisória", Tone.WAITING)
                if (item.status == "pending_clarification") Tag("em conferência", Tone.WAITING)
                Text(categoryText(item), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                when (item.classification) {
                    "suggested" -> Tag("categoria provisória", Tone.WAITING, dashed = true)
                    "missing" -> Tag("classificação pendente", Tone.NEUTRAL, dashed = true)
                }
                if (item.amountBasis == "installment" && item.installments != null) Tag("parcela de ${item.installments}x", Tone.NEUTRAL)
                item.payment?.let { Text("· " + paymentText(it.label, it.cardLast4), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
            }
        }
        Column(horizontalAlignment = Alignment.End) {
            when {
                item.amountBasis == "installment" -> {
                    Text("indisponível", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Text("valor integral", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                item.inTotal -> Text(Format.money(item.amount, hidden), style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.SemiBold)
                else -> {
                    Text(Format.money(item.amount, hidden), style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Text("fora do total", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
        }
    }
}

fun categoryText(item: ExpenseItem): String = item.category?.label ?: "Sem categoria"

fun paymentText(label: String, cardLast4: String?): String = label + (cardLast4?.let { " •••• $it" } ?: "")

@Composable
private fun ListSkeleton() {
    SectionCard(title = null) {
        Skeleton(160.dp, 16.dp)
        Skeleton(null, 12.dp)
    }
    Column(Modifier.padding(top = 12.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        repeat(4) { Skeleton(null, 56.dp) }
    }
}
