package dev.code04.lastro.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import dev.code04.lastro.R
import dev.code04.lastro.net.DayTotal
import dev.code04.lastro.net.MonthTotal

enum class BarStyle { SOLID, MUTED, STRIPED, DASHED, OUTLINE }

@Composable
fun Bar(fraction: Float, style: BarStyle, modifier: Modifier = Modifier) {
    val scheme = MaterialTheme.colorScheme
    Box(modifier.fillMaxWidth().height(10.dp).clip(RoundedCornerShape(5.dp)).background(scheme.surfaceContainerHigh)) {
        val filled = Modifier.fillMaxWidth(fraction.coerceIn(0.02f, 1f)).fillMaxHeight().clip(RoundedCornerShape(5.dp))
        when (style) {
            BarStyle.SOLID -> Box(filled.background(scheme.primary))
            BarStyle.MUTED -> Box(filled.background(Muted))
            BarStyle.STRIPED -> Box(filled.background(scheme.tertiaryContainer).stripes(scheme.tertiary, gap = 5.dp))
            BarStyle.DASHED -> Box(filled.dashedBorder(scheme.onSurfaceVariant, 5.dp, 1.5.dp))
            BarStyle.OUTLINE -> Box(filled.border(1.5.dp, scheme.secondary, RoundedCornerShape(5.dp)))
        }
    }
}

@Composable
fun BarRow(label: String, value: String, share: String?, fraction: Float, style: BarStyle, onClick: (() -> Unit)?, detail: String? = null) {
    val modifier = if (onClick != null) Modifier.clickable(onClickLabel = "Ver despesas de $label", onClick = onClick) else Modifier
    Column(
        modifier.fillMaxWidth().heightIn(min = 48.dp).padding(vertical = 8.dp).semantics(mergeDescendants = true) {},
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(
                buildString {
                    append(label)
                    if (detail != null) append(" ($detail)")
                },
                Modifier.weight(1f),
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.Medium,
            )
            Text(value, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold)
            if (share != null) {
                Text(share, Modifier.width(48.dp), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.End)
            }
        }
        Bar(fraction, style)
    }
}

val Muted = Color(0xFF6E8C7A)

@Composable
fun DailyColumns(days: List<DayTotal>, selected: String?, onSelect: (String) -> Unit, description: String) {
    val scheme = MaterialTheme.colorScheme
    val max = days.maxOfOrNull { it.gross.amount.minor }?.takeIf { it > 0 } ?: 1L
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Row(
            Modifier.fillMaxWidth().height(140.dp).semantics { contentDescription = description },
            horizontalArrangement = Arrangement.spacedBy(3.dp),
            verticalAlignment = Alignment.Bottom,
        ) {
            days.forEach { day ->
                val future = day.coverage == "future"
                val uncovered = day.coverage == "unknown" || day.coverage == "partial"
                Box(
                    Modifier
                        .weight(1f)
                        .fillMaxHeight()
                        .then(if (future) Modifier else Modifier.clickable(onClickLabel = Format.shortDate(day.date)) { onSelect(day.date) })
                        .clearAndSetSemantics { },
                ) {
                    if (uncovered) Box(Modifier.fillMaxSize().stripes(scheme.tertiary.copy(alpha = 0.38f), gap = 7.dp))
                    when {
                        future -> Box(Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(2.dp).background(scheme.outline.copy(alpha = 0.6f)))
                        day.gross.count == 0 -> if (!uncovered) Box(Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(3.dp).background(Muted))
                        else -> Box(
                            Modifier
                                .align(Alignment.BottomCenter)
                                .fillMaxWidth()
                                .fillMaxHeight((day.gross.amount.minor.toFloat() / max).coerceIn(0.02f, 1f))
                                .clip(RoundedCornerShape(topStart = 2.dp, topEnd = 2.dp))
                                .background(if (day.date == selected) scheme.onPrimaryContainer else scheme.primary),
                        )
                    }
                }
            }
        }
        Row(Modifier.fillMaxWidth().clearAndSetSemantics { }) {
            val labels = days.mapIndexedNotNull { index, day -> if (index == 0 || (index + 1) % 7 == 1 || index == days.lastIndex) index to day.date.takeLast(2).trimStart('0') else null }
            Box(Modifier.fillMaxWidth()) {
                labels.forEach { (index, label) ->
                    val bias = if (days.size <= 1) -1f else -1f + 2f * index / (days.size - 1)
                    Text(label, Modifier.align(androidx.compose.ui.BiasAlignment(bias, 0f)), style = MaterialTheme.typography.labelSmall, color = scheme.onSurfaceVariant)
                }
            }
        }
    }
}

@Composable
fun ChartLegend(items: List<Pair<String, @Composable () -> Unit>>) {
    androidx.compose.foundation.layout.FlowRow(horizontalArrangement = Arrangement.spacedBy(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        items.forEach { (label, swatch) ->
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                swatch()
                Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

@Composable
fun SolidSwatch(color: Color = MaterialTheme.colorScheme.primary) {
    Box(Modifier.size(10.dp).clip(RoundedCornerShape(2.dp)).background(color))
}

@Composable
fun LineSwatch(color: Color = Muted) {
    Box(Modifier.size(width = 10.dp, height = 3.dp).background(color))
}

@Composable
fun StripedSwatch() {
    Box(Modifier.size(10.dp).stripes(MaterialTheme.colorScheme.tertiary, gap = 3.dp, width = 1.5.dp))
}

@Composable
fun InProgressSwatch() {
    val scheme = MaterialTheme.colorScheme
    Box(
        Modifier.size(10.dp).clip(RoundedCornerShape(2.dp)).background(scheme.primaryContainer)
            .stripes(scheme.primary.copy(alpha = 0.55f), gap = 3.dp, width = 1.5.dp).border(1.dp, scheme.primary, RoundedCornerShape(2.dp)),
    )
}

@Composable
fun OutlineSwatch(color: Color = MaterialTheme.colorScheme.secondary) {
    Box(Modifier.size(width = 16.dp, height = 8.dp).border(1.5.dp, color, RoundedCornerShape(4.dp)))
}

@Composable
fun MonthColumns(months: List<MonthTotal>, hidden: Boolean) {
    val scheme = MaterialTheme.colorScheme
    val max = months.maxOfOrNull { it.gross.amount.minor }?.takeIf { it > 0 } ?: 1L
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(Modifier.fillMaxWidth().height(172.dp), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.Bottom) {
            months.forEach { month ->
                val noData = month.coverage == "none" && month.gross.count == 0
                Column(Modifier.weight(1f).fillMaxHeight().clearAndSetSemantics { }, verticalArrangement = Arrangement.Bottom, horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(
                        when {
                            noData -> "sem dados"
                            hidden -> "••••"
                            else -> thousands(month.gross.amount.minor)
                        },
                        style = MaterialTheme.typography.labelSmall,
                        color = if (month.inProgress) scheme.onSurface else scheme.onSurfaceVariant,
                        fontWeight = if (month.inProgress) FontWeight.SemiBold else FontWeight.Normal,
                        textAlign = TextAlign.Center,
                    )
                    Box(Modifier.height(4.dp))
                    val fraction = if (noData) 0.25f else (month.gross.amount.minor.toFloat() / max).coerceIn(0.02f, 1f)
                    val shape = RoundedCornerShape(topStart = 4.dp, topEnd = 4.dp)
                    val bar = Modifier.fillMaxWidth().fillMaxHeight(fraction * 0.82f).clip(shape)
                    when {
                        noData -> Box(bar.dashedBorder(scheme.outline, 4.dp, 1.5.dp))
                        month.inProgress -> Box(bar.background(scheme.primaryContainer).stripes(scheme.primary.copy(alpha = 0.55f), gap = 6.dp).border(1.5.dp, scheme.primary, shape))
                        else -> Box(bar.background(scheme.primary))
                    }
                }
            }
        }
        Row(Modifier.fillMaxWidth().clearAndSetSemantics { }, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            months.forEach { month ->
                Column(Modifier.weight(1f), horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(
                        Format.monthShort(month.month),
                        style = MaterialTheme.typography.labelMedium,
                        color = if (month.inProgress) scheme.onSurface else scheme.onSurfaceVariant,
                        fontWeight = if (month.inProgress) FontWeight.Medium else FontWeight.Normal,
                    )
                    if (month.inProgress) Text("até ${month.to.takeLast(2).trimStart('0')}", style = MaterialTheme.typography.labelSmall, color = scheme.onSurfaceVariant)
                }
            }
        }
    }
}

fun thousands(minor: Long): String {
    val tenths = Math.round(minor / 10_000.0)
    return if (minor >= 100_000) "${tenths / 10},${tenths % 10} mil" else String.format(java.util.Locale.ROOT, "%,d", minor / 100).replace(',', '.')
}

@Composable
fun StageSegments(segments: List<Pair<Int, @Composable (Modifier) -> Unit>>, description: String) {
    val total = segments.sumOf { it.first }.coerceAtLeast(1)
    Row(
        Modifier.fillMaxWidth().height(14.dp).clip(RoundedCornerShape(7.dp)).semantics { contentDescription = description },
        horizontalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        segments.filter { it.first > 0 }.forEach { (count, draw) ->
            draw(Modifier.weight(count.toFloat() / total).fillMaxHeight())
        }
        if (segments.all { it.first == 0 }) Box(Modifier.weight(1f).fillMaxHeight().background(MaterialTheme.colorScheme.surfaceContainerHigh))
    }
}

@Composable
fun ChevronIcon() {
    AppIcon(R.drawable.ic_chevron_right, size = 18.dp)
}
