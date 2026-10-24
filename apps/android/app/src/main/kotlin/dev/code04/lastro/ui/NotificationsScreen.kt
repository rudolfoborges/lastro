package dev.code04.lastro.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import dev.code04.lastro.history.HistoryItem
import dev.code04.lastro.history.ItemStatus
import dev.code04.lastro.history.Labels

@Composable
fun NotificationsScreen(items: List<HistoryItem>, server: ServerView, modifier: Modifier = Modifier) {
    LazyColumn(
        modifier.fillMaxSize(),
        contentPadding = PaddingValues(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        item {
            Text(
                serverLine(server),
                style = MaterialTheme.typography.bodySmall,
                color = if (server.problem.isEmpty()) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.error,
            )
        }
        if (items.isEmpty()) {
            item {
                Notice(
                    "Nenhuma notificação capturada ainda. As compras do Google Wallet aparecem aqui assim que chegam ao celular.",
                    Tone.NEUTRAL,
                )
            }
        }
        items(items, key = { it.capture.id }) { NotificationCard(it) }
    }
}

@Composable
private fun NotificationCard(item: HistoryItem) {
    val capture = item.capture
    Card(
        modifier = Modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text(
                    formatTime(capture.postedAt.takeIf { it > 0 } ?: capture.capturedAt),
                    modifier = Modifier.weight(1f),
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                StatusChip(Labels.status(item.status), item.status.tone())
            }
            if (capture.title.isNotEmpty()) {
                Text(capture.title, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            }
            val body = capture.bigText.ifEmpty { capture.text }
            if (body.isNotEmpty()) {
                Text(body, style = MaterialTheme.typography.bodyMedium, maxLines = 3, overflow = TextOverflow.Ellipsis)
            }
            Labels.details(item.status)?.let { details ->
                Text(details, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.primary)
            }
        }
    }
}

private fun ItemStatus.tone(): Tone = when (this) {
    is ItemStatus.Processed -> if (reading.eventType == "unknown") Tone.WAITING else Tone.GOOD
    ItemStatus.AwaitingCase, ItemStatus.Queued, ItemStatus.TrackingPaused -> Tone.WAITING
    is ItemStatus.Refused, ItemStatus.Discarded -> Tone.BAD
    ItemStatus.Sent, ItemStatus.OnlyOnPhone, ItemStatus.Unknown -> Tone.NEUTRAL
}
