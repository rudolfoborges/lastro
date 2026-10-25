package dev.code04.lastro.ui

import androidx.annotation.DrawableRes
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.clipRect
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import dev.code04.lastro.R
import dev.code04.lastro.data.Problem
import dev.code04.lastro.data.ProblemKind
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

val LocalHidden = compositionLocalOf { false }

enum class Tone { GOOD, WAITING, BAD, NEUTRAL }

data class ToneColors(val container: Color, val content: Color)

@Composable
fun Tone.colors(): ToneColors {
    val scheme = MaterialTheme.colorScheme
    return when (this) {
        Tone.GOOD -> ToneColors(scheme.primaryContainer, scheme.onPrimaryContainer)
        Tone.WAITING -> ToneColors(scheme.tertiaryContainer, scheme.onTertiaryContainer)
        Tone.BAD -> ToneColors(scheme.errorContainer, scheme.onErrorContainer)
        Tone.NEUTRAL -> ToneColors(scheme.surfaceContainerHighest, scheme.onSurfaceVariant)
    }
}

@Composable
fun Tone.accent(): Color = when (this) {
    Tone.GOOD -> MaterialTheme.colorScheme.primary
    Tone.WAITING -> MaterialTheme.colorScheme.tertiary
    Tone.BAD -> MaterialTheme.colorScheme.error
    Tone.NEUTRAL -> MaterialTheme.colorScheme.onSurfaceVariant
}

private val timeFormat = DateTimeFormatter.ofPattern("dd/MM HH:mm").withZone(ZoneId.systemDefault())
private val clockFormat = DateTimeFormatter.ofPattern("HH:mm").withZone(ZoneId.systemDefault())

fun formatTime(millis: Long): String = if (millis == 0L) "nunca" else timeFormat.format(Instant.ofEpochMilli(millis))

fun formatClock(instant: Instant): String = clockFormat.format(instant)

fun formatMoment(instant: Instant): String = timeFormat.format(instant)

@Composable
fun SectionCard(title: String?, modifier: Modifier = Modifier, subtitle: String? = null, content: @Composable ColumnScope.() -> Unit) {
    Card(
        modifier = modifier.fillMaxWidth(),
        shape = RoundedCornerShape(24.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
    ) {
        Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            if (title != null) {
                Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                    if (subtitle != null) Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            content()
        }
    }
}

@Composable
fun Overline(text: String) {
    Text(text.uppercase(), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.secondary, fontWeight = FontWeight.Medium)
}

@Composable
fun AppIcon(@DrawableRes icon: Int, modifier: Modifier = Modifier, tint: Color = MaterialTheme.colorScheme.onSurfaceVariant, size: Dp = 20.dp) {
    Icon(painterResource(icon), contentDescription = null, modifier = modifier.size(size), tint = tint)
}

@Composable
fun StatusChip(text: String, tone: Tone, @DrawableRes icon: Int? = null) {
    val colors = tone.colors()
    Row(
        Modifier.background(colors.container, RoundedCornerShape(50)).padding(start = if (icon != null) 8.dp else 12.dp, end = 12.dp, top = 4.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        if (icon != null) AppIcon(icon, tint = colors.content, size = 16.dp)
        Text(text, color = colors.content, style = MaterialTheme.typography.labelLarge)
    }
}

@Composable
fun Tag(text: String, tone: Tone, dashed: Boolean = false) {
    val accent = tone.accent()
    val modifier = if (dashed) {
        Modifier.dashedBorder(accent, 6.dp)
    } else {
        Modifier.background(tone.colors().container, RoundedCornerShape(6.dp))
    }
    Text(
        text,
        modifier = modifier.padding(horizontal = 6.dp, vertical = 1.dp),
        color = if (dashed) accent else tone.colors().content,
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.Medium,
    )
}

@Composable
fun StatusRow(label: String, value: String, tone: Tone, detail: String? = null, action: (@Composable RowScope.() -> Unit)? = null) {
    Row(Modifier.fillMaxWidth().heightIn(min = 44.dp), verticalAlignment = Alignment.CenterVertically) {
        val icon = when (tone) {
            Tone.GOOD -> R.drawable.ic_check
            Tone.WAITING -> R.drawable.ic_alert
            Tone.BAD -> R.drawable.ic_close
            Tone.NEUTRAL -> R.drawable.ic_clock
        }
        AppIcon(icon, tint = tone.accent())
        Column(Modifier.padding(start = 12.dp).weight(1f)) {
            Text(label, style = MaterialTheme.typography.bodyMedium)
            if (detail != null) Text(detail, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Text(value, style = MaterialTheme.typography.bodyMedium, color = if (tone == Tone.GOOD || tone == Tone.NEUTRAL) MaterialTheme.colorScheme.onSurfaceVariant else tone.accent())
        action?.invoke(this)
    }
}

@Composable
fun InfoRow(label: String, value: String, valueColor: Color = MaterialTheme.colorScheme.onSurfaceVariant) {
    Row(Modifier.fillMaxWidth().heightIn(min = 36.dp), horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(label, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, style = MaterialTheme.typography.bodyMedium, color = valueColor)
    }
}

@Composable
fun ToggleRow(label: String, description: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().heightIn(min = 64.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f).padding(end = 12.dp)) {
            Text(label, style = MaterialTheme.typography.bodyLarge)
            Text(description, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Switch(checked = checked, onCheckedChange = onChange)
    }
}

@Composable
fun Notice(text: String, tone: Tone, @DrawableRes icon: Int = R.drawable.ic_info, title: String? = null, action: (@Composable () -> Unit)? = null) {
    val colors = tone.colors()
    Row(
        Modifier.fillMaxWidth().background(colors.container, RoundedCornerShape(16.dp)).padding(14.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        AppIcon(icon, tint = tone.accent())
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            if (title != null) Text(title, color = colors.content, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            Text(text, color = colors.content, style = MaterialTheme.typography.bodyMedium)
            if (action != null) action()
        }
    }
}

@Composable
fun LinkButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    TextButton(onClick = onClick, modifier = modifier.heightIn(min = 44.dp)) {
        Text(text, style = MaterialTheme.typography.labelLarge)
        AppIcon(R.drawable.ic_chevron_right, tint = MaterialTheme.colorScheme.primary, size = 18.dp)
    }
}

@Composable
fun PrimaryButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Button(onClick = onClick, modifier = modifier.heightIn(min = 44.dp)) { Text(text) }
}

@Composable
fun TonalButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Button(
        onClick = onClick,
        modifier = modifier.heightIn(min = 44.dp),
        colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.secondaryContainer, contentColor = MaterialTheme.colorScheme.onSecondaryContainer),
    ) { Text(text) }
}

@Composable
fun ProblemCard(problem: Problem, onRetry: () -> Unit, onOpenSettings: () -> Unit) {
    val tone = if (problem.kind == ProblemKind.NETWORK) Tone.NEUTRAL else Tone.BAD
    val icon = if (problem.kind == ProblemKind.NETWORK) R.drawable.ic_wifi_off else R.drawable.ic_alert
    Notice(
        text = problem.message,
        tone = tone,
        icon = icon,
        title = if (problem.kind == ProblemKind.NETWORK) "Não foi possível consultar agora" else null,
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            when (problem.kind) {
                ProblemKind.NOT_CONFIGURED, ProblemKind.NO_READ_ACCESS, ProblemKind.UNAUTHORIZED -> TonalButton("Abrir Configurações", onOpenSettings)
                ProblemKind.NOT_FOUND -> Unit
                ProblemKind.NETWORK, ProblemKind.SERVER -> TonalButton("Consultar de novo", onRetry)
            }
        }
    }
}

@Composable
fun Skeleton(width: Dp?, height: Dp) {
    val base = if (width == null) Modifier.fillMaxWidth() else Modifier.size(width, height)
    Box(base.heightIn(min = height, max = height).background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(8.dp)))
}

@Composable
fun Dot(color: Color, size: Dp = 8.dp) {
    Box(Modifier.size(size).background(color, CircleShape))
}

@Composable
fun IconCircle(@DrawableRes icon: Int, tone: Tone, size: Dp = 36.dp) {
    Box(Modifier.size(size).clip(CircleShape).background(tone.colors().container), contentAlignment = Alignment.Center) {
        AppIcon(icon, tint = tone.accent(), size = size / 2)
    }
}

fun Modifier.stripes(color: Color, gap: Dp = 6.dp, width: Dp = 2.dp): Modifier = drawBehind {
    val step = gap.toPx()
    val stroke = width.toPx()
    clipRect {
        var x = -size.height
        while (x < size.width) {
            drawLine(color, Offset(x, size.height), Offset(x + size.height, 0f), strokeWidth = stroke)
            x += step
        }
    }
}

fun Modifier.dashedBorder(color: Color, radius: Dp, width: Dp = 1.dp): Modifier = drawBehind {
    val stroke = width.toPx()
    val dash = 4.dp.toPx()
    val corner = radius.toPx()
    drawRoundRect(
        color = color,
        cornerRadius = androidx.compose.ui.geometry.CornerRadius(corner, corner),
        style = androidx.compose.ui.graphics.drawscope.Stroke(
            width = stroke,
            pathEffect = androidx.compose.ui.graphics.PathEffect.dashPathEffect(floatArrayOf(dash, dash)),
        ),
    )
}

fun Modifier.tappable(label: String, onClick: () -> Unit): Modifier = clickable(onClickLabel = label, role = Role.Button, onClick = onClick)

fun Modifier.outlined(color: Color, radius: Dp = 22.dp): Modifier = border(1.dp, color, RoundedCornerShape(radius))
