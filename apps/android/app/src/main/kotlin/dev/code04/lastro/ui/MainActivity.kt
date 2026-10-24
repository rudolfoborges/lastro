package dev.code04.lastro.ui

import android.annotation.SuppressLint
import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import android.provider.Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS
import android.provider.Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.core.net.toUri
import dev.code04.lastro.data.LocalStore
import dev.code04.lastro.data.Settings
import dev.code04.lastro.ui.theme.LastroTheme
import dev.code04.lastro.work.Scheduling

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
        )
        super.onCreate(savedInstanceState)
        val dashboard = Dashboard(this)
        setContent {
            LastroTheme {
                LastroApp(dashboard = dashboard, actions = actions())
            }
        }
    }

    private fun actions() = SettingsActions(
        openNotificationAccess = { startActivity(Intent(ACTION_NOTIFICATION_LISTENER_SETTINGS)) },
        requestBatteryExemption = ::requestBatteryExemption,
        saveDestination = { url, token, allowlist ->
            val settings = Settings(this)
            settings.serverUrl = url
            if (token.isNotBlank()) settings.saveApiKey(token)
            settings.allowlist = allowlist.split(',').toSet()
            settings.lastError = ""
            refreshScheduling()
        },
        forgetApiKey = {
            Settings(this).forgetApiKey()
            refreshScheduling()
        },
        setSending = { enabled ->
            Settings(this).sendingEnabled = enabled
            refreshScheduling()
        },
        setPaused = { paused ->
            Settings(this).paused = paused
            refreshScheduling()
        },
        exportCaptures = {
            val share = Intent(Intent.ACTION_SEND).apply {
                type = "application/json"
                putExtra(Intent.EXTRA_SUBJECT, "Notificações capturadas pelo Lastro")
                putExtra(Intent.EXTRA_TEXT, LocalStore.get(this@MainActivity).capturesAsJson())
            }
            startActivity(Intent.createChooser(share, "Exportar amostras"))
        },
        clearCaptures = { LocalStore.get(this).clearCaptures() },
    )

    @SuppressLint("BatteryLife")
    private fun requestBatteryExemption() {
        startActivity(Intent(ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, "package:$packageName".toUri()))
    }

    private fun refreshScheduling() {
        if (Settings(this).readyToSend) {
            Scheduling.keepHeartbeat(this)
            Scheduling.deliverSoon(this)
        } else {
            Scheduling.stopHeartbeat(this)
        }
    }
}
