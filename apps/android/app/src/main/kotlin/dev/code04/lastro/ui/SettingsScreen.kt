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
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import dev.code04.lastro.BuildConfig
import dev.code04.lastro.R
import dev.code04.lastro.data.AppRepository
import dev.code04.lastro.data.ProblemKind
import dev.code04.lastro.net.Whoami

class SettingsActions(
    val openNotificationAccess: () -> Unit,
    val requestBatteryExemption: () -> Unit,
    val saveDestination: (url: String, token: String, allowlist: String) -> Unit,
    val forgetApiKey: () -> Unit,
    val setSending: (Boolean) -> Unit,
    val setPaused: (Boolean) -> Unit,
    val setHideValues: (Boolean) -> Unit,
    val setProtectPreview: (Boolean) -> Unit,
    val exportCaptures: () -> Unit,
    val clearCaptures: () -> Unit,
)

@Composable
fun SettingsScreen(
    snapshot: StatusSnapshot,
    repository: AppRepository,
    actions: SettingsActions,
    onChanged: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var test by remember { mutableIntStateOf(0) }
    val access = load(snapshot.hasApiKey, snapshot.serverUrl, test) { repository.whoami() }
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(start = 16.dp, end = 16.dp, top = 4.dp, bottom = 24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(
            "Estas opções mudam só o uso do app. Nenhuma delas altera despesas.",
            Modifier.padding(horizontal = 4.dp),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        SectionCard(title = "Coleta") {
            ToggleRow(
                "Coletar notificações",
                if (snapshot.paused) "Pausada. Toque para retomar." else "Pausar afeta só novas capturas. Não apaga o histórico nem altera despesas.",
                !snapshot.paused,
            ) {
                actions.setPaused(!it)
                onChanged()
            }
            if (snapshot.paused) {
                Notice(
                    "Compras feitas com a coleta pausada não são capturadas, e retomar não recupera o período parado. O histórico continua disponível.",
                    Tone.WAITING,
                    R.drawable.ic_pause,
                )
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            StatusRow(
                "Acesso às notificações",
                if (snapshot.notificationAccess) "permitido" else "desligado",
                if (snapshot.notificationAccess) Tone.GOOD else Tone.BAD,
            ) {
                androidx.compose.material3.TextButton(onClick = actions.openNotificationAccess) { Text("Abrir") }
            }
            StatusRow(
                "Economia de bateria",
                if (snapshot.batteryUnrestricted) "sem restrição" else "pode interromper",
                if (snapshot.batteryUnrestricted) Tone.GOOD else Tone.WAITING,
            ) {
                if (!snapshot.batteryUnrestricted) androidx.compose.material3.TextButton(onClick = actions.requestBatteryExemption) { Text("Liberar") }
            }
            if (!snapshot.notificationAccess) {
                Text("Sem acesso às notificações, o app não declara a coleta ativa: nada é capturado.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            }
        }
        ConnectionCard(snapshot, access.state, actions, onTest = { test++ }, onChanged = onChanged)
        SectionCard(title = "Privacidade") {
            ToggleRow(
                "Ocultar valores",
                "Esconde valores em resumos, gráficos, listas e detalhes, e o texto das notificações.",
                snapshot.hideValues,
            ) {
                actions.setHideValues(it)
                onChanged()
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            ToggleRow(
                "Proteger a prévia do app",
                "Valores e descrições ficam ilegíveis na tela de apps recentes.",
                snapshot.protectPreview,
            ) {
                actions.setProtectPreview(it)
                onChanged()
            }
        }
        SectionCard(title = "Histórico no celular") {
            InfoRow("Notificações guardadas", snapshot.captureCount.toString(), MaterialTheme.colorScheme.onSurface)
            Text(
                "As últimas capturas ficam guardadas só neste celular, já sem números de cartão e códigos.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = actions.exportCaptures) { Text("Exportar amostras") }
                OutlinedButton(onClick = {
                    actions.clearCaptures()
                    onChanged()
                }) { Text("Apagar do celular", color = MaterialTheme.colorScheme.error) }
            }
        }
        Text(
            "Lastro para Android · versão ${BuildConfig.VERSION_NAME}",
            Modifier.fillMaxWidth(),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = androidx.compose.ui.text.style.TextAlign.Center,
        )
    }
}

@Composable
private fun ConnectionCard(
    snapshot: StatusSnapshot,
    access: Load<Whoami>,
    actions: SettingsActions,
    onTest: () -> Unit,
    onChanged: () -> Unit,
) {
    var url by remember(snapshot.serverUrl) { mutableStateOf(snapshot.serverUrl) }
    var token by remember { mutableStateOf("") }
    var allowlist by remember(snapshot.allowlist) { mutableStateOf(snapshot.allowlist) }
    SectionCard(title = null) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Conexão", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
            when {
                !snapshot.hasDestination -> StatusChip("não configurada", Tone.NEUTRAL)
                access is Load.Ready && access.value.canRead && access.value.canSend -> StatusChip("pronta", Tone.GOOD, R.drawable.ic_check)
                access is Load.Ready -> StatusChip("permissão incompleta", Tone.WAITING, R.drawable.ic_alert)
                access is Load.Failed && access.problem.kind == ProblemKind.NETWORK -> StatusChip("sem conexão", Tone.NEUTRAL, R.drawable.ic_wifi_off)
                access is Load.Failed -> StatusChip("credencial recusada", Tone.BAD, R.drawable.ic_alert)
                else -> StatusChip("verificando…", Tone.NEUTRAL)
            }
        }
        if (access is Load.Ready) {
            val allowed = buildList {
                if (access.value.canSend) add("enviar notificações")
                if (access.value.canRead) add("consultar despesas")
            }
            InfoRow("Permite", allowed.joinToString(" e ").ifEmpty { "nada que o app usa" }, MaterialTheme.colorScheme.onSurface)
            if (!access.value.canRead) Text("Para ver despesas, use uma credencial com expenses:read além de wallet:ingest.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.tertiary)
        }
        OutlinedTextField(url, { url = it }, label = { Text("Servidor") }, singleLine = true, modifier = Modifier.fillMaxWidth())
        OutlinedTextField(
            token,
            { token = it },
            label = { Text(if (snapshot.hasApiKey) "Credencial guardada · digite só para substituir" else "Credencial (expenses:read e wallet:ingest)") },
            singleLine = true,
            visualTransformation = PasswordVisualTransformation(),
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(allowlist, { allowlist = it }, label = { Text("Apps capturados") }, modifier = Modifier.fillMaxWidth())
        ToggleRow("Enviar notificações ao Lastro", "Manda cada notificação capturada para a Case ler.", snapshot.sendingEnabled) {
            actions.setSending(it)
            onChanged()
        }
        InfoRow("Dispositivo", snapshot.deviceId)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            PrimaryButton("Salvar", {
                actions.saveDestination(url, token, allowlist)
                token = ""
                onChanged()
                onTest()
            })
            OutlinedButton(onClick = onTest) { Text("Testar conexão") }
        }
        if (snapshot.hasApiKey) {
            androidx.compose.material3.TextButton(onClick = {
                actions.forgetApiKey()
                onChanged()
                onTest()
            }) { Text("Remover credencial", color = MaterialTheme.colorScheme.error) }
        }
    }
}
