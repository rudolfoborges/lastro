package dev.code04.lastro.ui

import android.content.Context
import dev.code04.lastro.data.CaptureRecord
import dev.code04.lastro.data.LocalStore
import dev.code04.lastro.data.Settings
import dev.code04.lastro.net.LastroClient
import dev.code04.lastro.net.ServerNotification
import dev.code04.lastro.net.ServerNotifications
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.IOException

data class StatusSnapshot(
    val serverUrl: String,
    val deviceId: String,
    val hasApiKey: Boolean,
    val hasDestination: Boolean,
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

sealed interface ServerFetch {
    data class Done(val notifications: List<ServerNotification>) : ServerFetch
    data class Failed(val message: String) : ServerFetch
    data object NotConfigured : ServerFetch
}

class Dashboard(context: Context) {
    private val context = context.applicationContext

    fun snapshot(): StatusSnapshot {
        val settings = Settings(context)
        val store = LocalStore.get(context)
        return StatusSnapshot(
            serverUrl = settings.serverUrl,
            deviceId = settings.deviceId,
            hasApiKey = settings.hasApiKey,
            hasDestination = settings.hasDestination,
            sendingEnabled = settings.sendingEnabled,
            paused = settings.paused,
            allowlist = settings.allowlist.sorted().joinToString(", "),
            notificationAccess = DeviceChecks.hasNotificationAccess(context),
            batteryUnrestricted = DeviceChecks.isBatteryUnrestricted(context),
            queueSize = store.queueSize(),
            captureCount = store.captureCount(),
            lastSentAt = settings.lastSentAt,
            lastHeartbeatAt = settings.lastHeartbeatAt,
            serverTracking = settings.serverTracking,
            lastError = settings.lastError,
        )
    }

    fun captures(): List<CaptureRecord> = LocalStore.get(context).history(ServerNotifications.LIMIT)

    suspend fun serverNotifications(): ServerFetch = withContext(Dispatchers.IO) {
        val settings = Settings(context)
        val apiKey = settings.apiKey
        if (!settings.hasDestination || apiKey == null) return@withContext ServerFetch.NotConfigured
        try {
            val response = LastroClient(settings.serverUrl, apiKey).get(ServerNotifications.path(settings.deviceId))
            when (response.status) {
                in 200..299 -> ServerFetch.Done(ServerNotifications.parse(response.body))
                401, 403 -> ServerFetch.Failed("Credencial recusada (${response.status}). Confira a chave em Ajustes.")
                else -> ServerFetch.Failed("Servidor respondeu ${response.status} ${response.errorCode.orEmpty()}".trim())
            }
        } catch (e: IOException) {
            ServerFetch.Failed("Falha de rede: ${e.message.orEmpty()}")
        }
    }
}
