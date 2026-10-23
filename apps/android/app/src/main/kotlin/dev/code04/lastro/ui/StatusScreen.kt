package dev.code04.lastro.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

data class StatusSnapshot(
    val serverUrl: String,
    val deviceId: String,
    val hasApiKey: Boolean,
    val sendingEnabled: Boolean,
    val paused: Boolean,
    val allowlist: String,
    val notificationAccess: Boolean,
    val batteryUnrestricted: Boolean,
    val queueSize: Int,
    val captureCount: Int,
    val lastSentAt: Long,
    val lastHeartbeatAt: Long,
    val serverTracking: String,
    val lastError: String,
)

class StatusActions(
    val openNotificationAccess: () -> Unit,
    val requestBatteryExemption: () -> Unit,
    val saveDestination: (url: String, token: String, allowlist: String) -> Unit,
    val forgetApiKey: () -> Unit,
    val setSending: (Boolean) -> Unit,
    val setPaused: (Boolean) -> Unit,
    val exportCaptures: () -> Unit,
    val clearCaptures: () -> Unit,
)

private val timeFormat = DateTimeFormatter.ofPattern("dd/MM HH:mm").withZone(ZoneId.systemDefault())

private fun formatTime(millis: Long): String = if (millis == 0L) "nunca" else timeFormat.format(Instant.ofEpochMilli(millis))

@Composable
fun StatusScreen(actions: StatusActions, snapshot: () -> StatusSnapshot) {
    var state by remember { mutableStateOf(snapshot()) }
    val refresh = { state = snapshot() }
    LifecycleResumeEffect(Unit) {
        refresh()
        onPauseOrDispose { }
    }
    var url by remember { mutableStateOf(state.serverUrl) }
    var token by remember { mutableStateOf("") }
    var allowlist by remember { mutableStateOf(state.allowlist) }

    Column(
        modifier = Modifier.safeDrawingPadding().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text("Lastro", style = MaterialTheme.typography.headlineMedium)

        Section("Captura")
        Line("Acesso às notificações", if (state.notificationAccess) "ativo" else "desligado")
        if (!state.notificationAccess) Button(onClick = actions.openNotificationAccess) { Text("Permitir acesso às notificações") }
        Line("Economia de bateria", if (state.batteryUnrestricted) "sem restrição" else "pode interromper a captura")
        if (!state.batteryUnrestricted) OutlinedButton(onClick = actions.requestBatteryExemption) { Text("Liberar da economia de bateria") }
        Toggle("Pausar captura e envio", state.paused) {
            actions.setPaused(it)
            refresh()
        }
        Line("Amostras guardadas no celular", state.captureCount.toString())
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedButton(onClick = actions.exportCaptures) { Text("Exportar amostras") }
            OutlinedButton(onClick = {
                actions.clearCaptures()
                refresh()
            }) { Text("Apagar amostras") }
        }

        Section("Destino")
        OutlinedTextField(url, { url = it }, label = { Text("URL do servidor") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        OutlinedTextField(
            token,
            { token = it },
            label = { Text(if (state.hasApiKey) "Chave guardada (digite para trocar)" else "Chave da API (wallet:ingest)") },
            singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(allowlist, { allowlist = it }, label = { Text("Apps capturados") }, modifier = Modifier.fillMaxWidth())
        Line("Dispositivo", state.deviceId)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = {
                actions.saveDestination(url, token, allowlist)
                token = ""
                refresh()
            }) { Text("Salvar") }
            if (state.hasApiKey) {
                OutlinedButton(onClick = {
                    actions.forgetApiKey()
                    refresh()
                }) { Text("Remover chave") }
            }
        }
        Toggle("Enviar ao servidor", state.sendingEnabled) {
            actions.setSending(it)
            refresh()
        }

        Section("Envio")
        Line("Na fila", state.queueSize.toString())
        Line("Último envio", formatTime(state.lastSentAt))
        Line("Último sinal de vida", formatTime(state.lastHeartbeatAt))
        Line("Acompanhamento no servidor", state.serverTracking.ifEmpty { "desconhecido" })
        if (state.lastError.isNotEmpty()) Text(state.lastError, color = MaterialTheme.colorScheme.error)
        OutlinedButton(onClick = refresh) { Text("Atualizar") }
    }
}

@Composable
private fun Section(title: String) {
    HorizontalDivider()
    Text(title, style = MaterialTheme.typography.titleMedium)
}

@Composable
private fun Line(label: String, value: String) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(label)
        Text(value, style = MaterialTheme.typography.bodyMedium)
    }
}

@Composable
private fun Toggle(label: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
        Text(label)
        Switch(checked = checked, onCheckedChange = onChange)
    }
}
