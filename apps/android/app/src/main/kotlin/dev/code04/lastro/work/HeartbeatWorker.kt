package dev.code04.lastro.work

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import dev.code04.lastro.BuildConfig
import dev.code04.lastro.data.LocalStore
import dev.code04.lastro.data.Settings
import dev.code04.lastro.net.LastroClient
import dev.code04.lastro.net.Payloads
import dev.code04.lastro.ui.DeviceChecks
import java.io.IOException
import java.time.Instant

class HeartbeatWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val settings = Settings(applicationContext)
        val apiKey = settings.apiKey
        if (!settings.readyToSend || apiKey == null) return Result.success()
        val store = LocalStore.get(applicationContext)
        val body = Payloads.heartbeat(
            deviceId = settings.deviceId,
            clientVersion = BuildConfig.VERSION_NAME,
            notificationAccess = DeviceChecks.hasNotificationAccess(applicationContext),
            batteryUnrestricted = DeviceChecks.isBatteryUnrestricted(applicationContext),
            queueSize = store.queueSize(),
        )
        val response = try {
            LastroClient(settings.serverUrl, apiKey).post("/v1/wallet/heartbeat", Payloads.stamped(body, Instant.now()))
        } catch (e: IOException) {
            settings.lastError = "Falha de rede no sinal de vida: ${e.message.orEmpty()}"
            return Result.retry()
        }
        if (response.status !in 200..299) {
            settings.lastError = "Sinal de vida recusado (${response.status} ${response.errorCode.orEmpty()})."
            return Result.retry()
        }
        settings.lastHeartbeatAt = System.currentTimeMillis()
        settings.serverTracking = response.body?.optString("tracking").orEmpty()
        if (store.queueSize() > 0) Scheduling.deliverSoon(applicationContext)
        return Result.success()
    }
}
