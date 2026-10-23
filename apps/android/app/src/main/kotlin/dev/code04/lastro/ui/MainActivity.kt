package dev.code04.lastro.ui

import android.annotation.SuppressLint
import android.content.Intent
import android.os.Bundle
import android.provider.Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS
import android.provider.Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.core.net.toUri
import dev.code04.lastro.data.LocalStore
import dev.code04.lastro.data.Settings
import dev.code04.lastro.work.Scheduling

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            MaterialTheme {
                Surface {
                    StatusScreen(actions = actions(), snapshot = ::snapshot)
                }
            }
        }
    }

    private fun snapshot(): StatusSnapshot {
        val settings = Settings(this)
        val store = LocalStore.get(this)
        return StatusSnapshot(
            serverUrl = settings.serverUrl,
            deviceId = settings.deviceId,
            hasApiKey = settings.hasApiKey,
            sendingEnabled = settings.sendingEnabled,
            paused = settings.paused,
            allowlist = settings.allowlist.sorted().joinToString(", "),
            notificationAccess = DeviceChecks.hasNotificationAccess(this),
            batteryUnrestricted = DeviceChecks.isBatteryUnrestricted(this),
            queueSize = store.queueSize(),
            captureCount = store.captureCount(),
            lastSentAt = settings.lastSentAt,
            lastHeartbeatAt = settings.lastHeartbeatAt,
            serverTracking = settings.serverTracking,
            lastError = settings.lastError,
        )
    }

    private fun actions() = StatusActions(
        openNotificationAccess = { startActivity(Intent(ACTION_NOTIFICATION_LISTENER_SETTINGS)) },
        requestBatteryExemption = ::requestBatteryExemption,
        saveDestination = { url, token, allowlist ->
            val settings = Settings(this)
            settings.serverUrl = url
            if (token.isNotBlank()) settings.saveApiKey(token)
            settings.allowlist = allowlist.split(',').toSet()
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
