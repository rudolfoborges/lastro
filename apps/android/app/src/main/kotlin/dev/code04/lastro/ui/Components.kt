package dev.code04.lastro.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

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
fun Tone.dot(): Color = when (this) {
    Tone.GOOD -> MaterialTheme.colorScheme.primary
    Tone.WAITING -> MaterialTheme.colorScheme.tertiary
    Tone.BAD -> MaterialTheme.colorScheme.error
    Tone.NEUTRAL -> MaterialTheme.colorScheme.outline
}

private val timeFormat = DateTimeFormatter.ofPattern("dd/MM HH:mm").withZone(ZoneId.systemDefault())

fun formatTime(millis: Long): String = if (millis == 0L) "nunca" else timeFormat.format(Instant.ofEpochMilli(millis))

@Composable
fun SectionCard(title: String, modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    Card(
        modifier = modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(title, style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)
            content()
        }
    }
}

@Composable
fun StatusRow(label: String, value: String, tone: Tone) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(8.dp).background(tone.dot(), CircleShape))
        Text(label, Modifier.padding(start = 12.dp).weight(1f), style = MaterialTheme.typography.bodyMedium)
        Text(value, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
fun InfoRow(label: String, value: String) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(label, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
        Text(value, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
fun ToggleRow(label: String, description: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f).padding(end = 12.dp)) {
            Text(label, style = MaterialTheme.typography.bodyLarge)
            Text(description, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Switch(checked = checked, onCheckedChange = onChange)
    }
}

@Composable
fun StatusChip(text: String, tone: Tone) {
    val colors = tone.colors()
    Text(
        text,
        modifier = Modifier
            .background(colors.container, RoundedCornerShape(50))
            .padding(horizontal = 10.dp, vertical = 4.dp),
        color = colors.content,
        style = MaterialTheme.typography.labelMedium,
        fontWeight = FontWeight.Medium,
    )
}

@Composable
fun Notice(text: String, tone: Tone) {
    val colors = tone.colors()
    Text(
        text,
        modifier = Modifier
            .fillMaxWidth()
            .background(colors.container, RoundedCornerShape(12.dp))
            .padding(12.dp),
        color = colors.content,
        style = MaterialTheme.typography.bodyMedium,
    )
}
