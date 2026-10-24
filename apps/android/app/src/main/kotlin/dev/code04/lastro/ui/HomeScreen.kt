package dev.code04.lastro.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import dev.code04.lastro.history.History
import dev.code04.lastro.history.HistoryItem

@Composable
fun HomeScreen(
    snapshot: StatusSnapshot,
    items: List<HistoryItem>,
    server: ServerView,
    onOpenNotifications: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        if (snapshot.lastError.isNotEmpty()) Notice(snapshot.lastError, Tone.BAD)
        LatestCard(items, server, onOpenNotifications)
        CaptureCard(snapshot)
        ServerCard(snapshot)
    }
}

@Composable
private fun LatestCard(items: List<HistoryItem>, server: ServerView, onOpenNotifications: () -> Unit) {
    val summary = History.summarize(items)
    SectionCard("Últimas notificações") {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            Counter(summary.awaitingCase, "aguardando\na Case", MaterialTheme.colorScheme.tertiary)
            Counter(summary.processed, "processadas\npela Case", MaterialTheme.colorScheme.primary)
            Counter(summary.notSent, "não\nenviadas", MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Text(serverLine(server), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        TextButton(onClick = onOpenNotifications, modifier = Modifier.align(Alignment.End)) { Text("Ver notificações") }
    }
}

@Composable
private fun Counter(value: Int, label: String, color: Color) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Text(value.toString(), style = MaterialTheme.typography.displaySmall, color = color, fontWeight = FontWeight.SemiBold)
        Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center)
    }
}

fun serverLine(server: ServerView): String = when {
    server.problem.isNotEmpty() -> server.problem
    server.checkedAt > 0 -> "Situação na Case consultada em ${formatTime(server.checkedAt)}."
    else -> "Consultando a situação na Case…"
}

@Composable
private fun CaptureCard(snapshot: StatusSnapshot) {
    SectionCard("Captura no celular") {
        StatusRow(
            "Acesso às notificações",
            if (snapshot.notificationAccess) "ativo" else "desligado",
            if (snapshot.notificationAccess) Tone.GOOD else Tone.BAD,
        )
        StatusRow(
            "Economia de bateria",
            if (snapshot.batteryUnrestricted) "sem restrição" else "pode interromper",
            if (snapshot.batteryUnrestricted) Tone.GOOD else Tone.WAITING,
        )
        StatusRow("Captura", if (snapshot.paused) "pausada" else "ativa", if (snapshot.paused) Tone.WAITING else Tone.GOOD)
        StatusRow(
            "Envio ao servidor",
            when {
                !snapshot.hasDestination -> "sem destino"
                snapshot.sendingEnabled -> "ligado"
                else -> "desligado"
            },
            if (snapshot.sendingEnabled && snapshot.hasDestination) Tone.GOOD else Tone.WAITING,
        )
        if (!snapshot.notificationAccess || !snapshot.batteryUnrestricted || !snapshot.hasDestination) {
            Text(
                "Ajuste o que falta na aba Ajustes.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun ServerCard(snapshot: StatusSnapshot) {
    SectionCard("Servidor") {
        StatusRow("Acompanhamento", trackingLabel(snapshot.serverTracking), if (snapshot.serverTracking == "active") Tone.GOOD else Tone.WAITING)
        StatusRow(
            "Na fila de envio",
            snapshot.queueSize.toString(),
            if (snapshot.queueSize == 0) Tone.GOOD else Tone.WAITING,
        )
        InfoRow("Último sinal de vida", formatTime(snapshot.lastHeartbeatAt))
        InfoRow("Último envio", formatTime(snapshot.lastSentAt))
    }
}

private fun trackingLabel(tracking: String): String = when (tracking) {
    "active" -> "ativo"
    "paused" -> "pausado"
    "never_enabled" -> "nunca ativado"
    else -> "desconhecido"
}
