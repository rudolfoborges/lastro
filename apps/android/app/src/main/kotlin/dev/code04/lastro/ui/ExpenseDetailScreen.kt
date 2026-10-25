package dev.code04.lastro.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import dev.code04.lastro.R
import dev.code04.lastro.data.AppRepository
import dev.code04.lastro.net.ExpenseDetail

@Composable
fun ExpenseDetailScreen(
    repository: AppRepository,
    id: String,
    refreshKey: Int,
    onOpenCollection: () -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val loaded = load(id, refreshKey) { repository.expense(id) }
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(start = 16.dp, end = 16.dp, top = 4.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        when (val state = loaded.state) {
            Load.Loading -> {
                Skeleton(200.dp, 24.dp)
                Skeleton(160.dp, 40.dp)
                Skeleton(null, 120.dp)
            }
            is Load.Failed -> ProblemCard(state.problem, loaded.retry, onOpenSettings)
            is Load.Ready -> DetailContent(state.value, onOpenCollection)
        }
    }
}

@Composable
private fun DetailContent(detail: ExpenseDetail, onOpenCollection: () -> Unit) {
    val hidden = LocalHidden.current
    val item = detail.item
    val unknownTotal = item.amountBasis == "installment"
    Column(Modifier.padding(start = 4.dp, top = 8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(item.merchant ?: item.description, style = MaterialTheme.typography.titleLarge)
        if (unknownTotal) {
            Text("Valor integral indisponível", style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.SemiBold, color = MaterialTheme.colorScheme.onSurfaceVariant)
        } else {
            Text(Format.money(item.amount, hidden), style = MaterialTheme.typography.displaySmall, fontWeight = FontWeight.SemiBold)
        }
        val facts = listOfNotNull(
            item.date.takeIf { it.isNotEmpty() }?.let { "${Format.weekday(it).replaceFirstChar(Char::uppercase)}, ${Format.fullDate(it)}" + (item.time?.let { time -> " às $time" } ?: "") }
                ?: "data indisponível",
            item.payment?.let { paymentText(it.label, it.cardLast4) },
            item.installments?.let { "em $it parcelas" },
        )
        Text(facts.joinToString(" · "), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
    if (!item.inTotal) {
        Notice(
            when {
                unknownTotal -> "Fica fora do total bruto até o valor integral ser conhecido. Em Visão geral, aparece à parte em “Fora do total”."
                item.nature == "refund" -> "É um estorno: conta pela própria data e só reduz o total líquido."
                item.status == "provisional" -> "Despesa provisória: ainda não confirmada, fica fora do total bruto e aparece à parte."
                item.status == "pending_clarification" -> "Despesa em conferência: há uma dúvida aberta e ela fica fora do total."
                else -> "Este registro não entra no total bruto."
            },
            Tone.NEUTRAL,
        )
    }
    SectionCard(title = "Situação") {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Despesa", Modifier.weight(1f), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
            when (item.status) {
                "confirmed" -> StatusChip("Confirmada", Tone.GOOD, R.drawable.ic_check)
                "provisional" -> StatusChip("Provisória", Tone.WAITING, R.drawable.ic_clock)
                "pending_clarification" -> StatusChip("Em conferência", Tone.WAITING, R.drawable.ic_alert)
                "voided" -> StatusChip("Anulada", Tone.NEUTRAL)
                "merged" -> StatusChip("Unida a outra", Tone.NEUTRAL)
                else -> StatusChip(item.status, Tone.NEUTRAL)
            }
        }
        Text(
            when (item.status) {
                "confirmed" -> if (item.inTotal) "Valor e data conferidos. Entra no total bruto registrado." else "Confirmada, mas fora do total bruto."
                "provisional" -> "Ainda não confirmada por uma fatura ou débito."
                "pending_clarification" -> "Há uma dúvida aberta com a Case."
                else -> "Não entra em nenhum total."
            },
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("Categoria", Modifier.weight(1f), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(item.category?.label ?: "Sem categoria", style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium)
            when (item.classification) {
                "suggested" -> Tag("provisória", Tone.WAITING, dashed = true)
                "missing" -> Tag("pendente", Tone.NEUTRAL, dashed = true)
                else -> Text("concluída", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
        Text(
            when (item.classification) {
                "suggested" -> "Sugerida pela Case e ainda não concluída. Por isso entra em “Classificação provisória” no gráfico de categorias, e não em ${item.category?.label ?: "uma categoria"}."
                "missing" -> "Ainda sem categoria. Entra em “Sem categoria” no gráfico."
                else -> when (item.category?.origin) {
                    "rule" -> "Concluída por uma regra que você aprovou."
                    "user" -> "Confirmada por você."
                    else -> "Concluída pela Case."
                }
            },
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
    SectionCard(title = "Datas e valores") {
        InfoRow("Data da compra", item.date.takeIf { it.isNotEmpty() }?.let(Format::fullDate) ?: "indisponível", MaterialTheme.colorScheme.onSurface)
        InfoRow("Valor integral", if (unknownTotal) "indisponível" else Format.money(item.amount, hidden), MaterialTheme.colorScheme.onSurface)
        val plan = detail.plan
        if (plan != null) {
            plan.installmentAmount?.let { InfoRow("Valor da parcela", Format.money(it, hidden), MaterialTheme.colorScheme.onSurface) }
            InfoRow("Parcelas", "${plan.count}x · ${plan.charged} cobrada(s)", MaterialTheme.colorScheme.onSurface)
            if (plan.knownFuture + plan.estimated > 0) {
                InfoRow(
                    "Parcelas futuras",
                    buildList {
                        if (plan.knownFuture > 0) add("${plan.knownFuture} conhecidas")
                        if (plan.estimated > 0) add("${plan.estimated} estimadas")
                    }.joinToString(" · "),
                    MaterialTheme.colorScheme.onSurface,
                )
                Text("Compromissos futuros ficam fora do total por compra.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        } else if (unknownTotal) {
            InfoRow("Valor da parcela", Format.money(item.amount, hidden) + " · conhecido só por parcela", MaterialTheme.colorScheme.onSurface)
        }
        InfoRow("Cobrança na fatura", if (detail.onStatement) "conferida em uma fatura" else "ainda não disponível")
        if (detail.refunds.isNotEmpty()) InfoRow("Estornado", Format.money(detail.refunded, hidden), MaterialTheme.colorScheme.onSurface)
        detail.purpose?.let { InfoRow("Finalidade", it, MaterialTheme.colorScheme.onSurface) }
    }
    SectionCard(title = "Origem") {
        if (detail.notifications.isEmpty()) {
            Text(
                "Nenhuma notificação do Google Wallet ligada a esta despesa: associação não disponível.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        } else {
            Text(
                if (detail.notifications.size == 1) "Registrada a partir de 1 notificação do Google Wallet." else "Registrada a partir de ${detail.notifications.size} notificações do Google Wallet. Elas formam uma única despesa.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Column(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(14.dp))) {
                detail.notifications.forEachIndexed { index, notification ->
                    if (index > 0) HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                    Row(
                        Modifier.fillMaxWidth().heightIn(min = 56.dp).tappable("Abrir coleta", onOpenCollection).padding(horizontal = 14.dp, vertical = 10.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        AppIcon(R.drawable.ic_bell, tint = MaterialTheme.colorScheme.secondary, size = 18.dp)
                        Column(Modifier.weight(1f)) {
                            Text(if (notification.event == "updated") "Atualização da compra" else "Compra notificada", style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Medium)
                            Text(notification.postedAt?.let { "capturada em ${formatMoment(it)}" } ?: "horário indisponível", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        ChevronIcon()
                    }
                }
            }
        }
        InfoRow("Linha de fatura", if (detail.onStatement) "conferida" else "associação não disponível")
    }
    if (detail.openQuestions > 0) {
        Notice("Há ${Format.count(detail.openQuestions, "dúvida aberta", "dúvidas abertas")} sobre esta despesa na conversa com a Case.", Tone.WAITING, R.drawable.ic_alert)
    }
    Row(
        Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainerLow, RoundedCornerShape(20.dp)).outlined(MaterialTheme.colorScheme.outlineVariant, 20.dp).padding(16.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        AppIcon(R.drawable.ic_chat, tint = MaterialTheme.colorScheme.secondary)
        Text(
            "Para corrigir dados, concluir a categoria ou tirar uma dúvida, fale com a Case. O app só mostra o que já está registrado.",
            style = MaterialTheme.typography.bodyMedium,
        )
    }
    detail.lastChangeAt?.let { at ->
        val author = when (detail.lastChangeAuthor) {
            "agent" -> "pela Case"
            "user" -> "por você"
            "rule" -> "por uma regra"
            "system" -> "pelo Lastro"
            else -> ""
        }
        Text(
            "Última alteração em ${formatMoment(at)} $author".trim() + ".",
            Modifier.padding(horizontal = 4.dp),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}
