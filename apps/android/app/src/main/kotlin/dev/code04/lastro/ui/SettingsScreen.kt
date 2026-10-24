package dev.code04.lastro.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp

class SettingsActions(
    val openNotificationAccess: () -> Unit,
    val requestBatteryExemption: () -> Unit,
    val saveDestination: (url: String, token: String, allowlist: String) -> Unit,
    val forgetApiKey: () -> Unit,
    val setSending: (Boolean) -> Unit,
    val setPaused: (Boolean) -> Unit,
    val exportCaptures: () -> Unit,
    val clearCaptures: () -> Unit,
)

@Composable
fun SettingsScreen(snapshot: StatusSnapshot, actions: SettingsActions, onChanged: () -> Unit, modifier: Modifier = Modifier) {
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        SectionCard("Envio") {
            ToggleRow("Enviar ao servidor", "Manda cada notificação capturada para o Lastro.", snapshot.sendingEnabled) {
                actions.setSending(it)
                onChanged()
            }
            ToggleRow("Pausar captura e envio", "Para de ler notificações; a fila é mantida.", snapshot.paused) {
                actions.setPaused(it)
                onChanged()
            }
        }
        DestinationCard(snapshot, actions, onChanged)
        SectionCard("Permissões do Android") {
            StatusRow("Acesso às notificações", if (snapshot.notificationAccess) "ativo" else "desligado", if (snapshot.notificationAccess) Tone.GOOD else Tone.BAD)
            OutlinedButton(onClick = actions.openNotificationAccess, modifier = Modifier.fillMaxWidth()) { Text("Abrir acesso às notificações") }
            StatusRow(
                "Economia de bateria",
                if (snapshot.batteryUnrestricted) "sem restrição" else "pode interromper",
                if (snapshot.batteryUnrestricted) Tone.GOOD else Tone.WAITING,
            )
            if (!snapshot.batteryUnrestricted) {
                OutlinedButton(onClick = actions.requestBatteryExemption, modifier = Modifier.fillMaxWidth()) { Text("Liberar da economia de bateria") }
            }
        }
        SectionCard("Histórico no celular") {
            InfoRow("Notificações guardadas", snapshot.captureCount.toString())
            Text(
                "O texto já redigido das últimas ${snapshot.captureCount} capturas fica só no celular. Exporte para gerar amostras.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = actions.exportCaptures) { Text("Exportar amostras") }
                OutlinedButton(onClick = {
                    actions.clearCaptures()
                    onChanged()
                }) { Text("Apagar histórico") }
            }
        }
    }
}

@Composable
private fun DestinationCard(snapshot: StatusSnapshot, actions: SettingsActions, onChanged: () -> Unit) {
    var url by remember(snapshot.serverUrl) { mutableStateOf(snapshot.serverUrl) }
    var token by remember { mutableStateOf("") }
    var allowlist by remember(snapshot.allowlist) { mutableStateOf(snapshot.allowlist) }
    SectionCard("Destino") {
        OutlinedTextField(url, { url = it }, label = { Text("URL do servidor") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        OutlinedTextField(
            token,
            { token = it },
            label = { Text(if (snapshot.hasApiKey) "Chave guardada (digite para trocar)" else "Chave da API (wallet:ingest)") },
            singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(allowlist, { allowlist = it }, label = { Text("Apps capturados") }, modifier = Modifier.fillMaxWidth())
        InfoRow("Dispositivo", snapshot.deviceId)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Button(onClick = {
                actions.saveDestination(url, token, allowlist)
                token = ""
                onChanged()
            }) { Text("Salvar") }
            if (snapshot.hasApiKey) {
                OutlinedButton(onClick = {
                    actions.forgetApiKey()
                    onChanged()
                }) { Text("Remover chave") }
            }
        }
    }
}
