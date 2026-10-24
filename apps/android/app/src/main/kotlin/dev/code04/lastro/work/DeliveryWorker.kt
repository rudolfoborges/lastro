package dev.code04.lastro.work

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import dev.code04.lastro.data.LocalStore
import dev.code04.lastro.data.Settings
import dev.code04.lastro.net.DeliveryOutcome
import dev.code04.lastro.net.DeliveryReceipt
import dev.code04.lastro.net.LastroClient
import dev.code04.lastro.net.Payloads
import org.json.JSONObject
import java.io.IOException
import java.time.Instant

class DeliveryWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val settings = Settings(applicationContext)
        val apiKey = settings.apiKey
        if (!settings.readyToSend || apiKey == null) return Result.success()
        val client = LastroClient(settings.serverUrl, apiKey)
        val store = LocalStore.get(applicationContext)
        while (true) {
            val batch = store.pending(BATCH)
            if (batch.isEmpty()) return Result.success()
            for (item in batch) {
                val response = try {
                    client.post("/v1/wallet/notifications", Payloads.stamped(JSONObject(item.body), Instant.now()))
                } catch (e: IOException) {
                    settings.lastError = "Falha de rede: ${e.message.orEmpty()}"
                    return Result.retry()
                }
                when (DeliveryOutcome.of(response.status, response.errorCode)) {
                    DeliveryOutcome.DELIVERED -> {
                        store.delivered(item, DeliveryReceipt.from(response.body))
                        settings.lastSentAt = System.currentTimeMillis()
                        settings.lastError = ""
                    }
                    DeliveryOutcome.RETRY_LATER -> {
                        settings.lastError = "Servidor respondeu ${response.status} ${response.errorCode.orEmpty()}; tentando de novo."
                        return Result.retry()
                    }
                    DeliveryOutcome.STOP -> {
                        settings.lastError = "Credencial recusada (${response.status}). Confira a chave nas configurações."
                        return Result.failure()
                    }
                    DeliveryOutcome.DROP -> {
                        val reason = "Notificação recusada (${response.status}): ${response.body?.optString("message").orEmpty()}"
                        store.refused(item, reason)
                        settings.lastError = reason
                    }
                }
            }
        }
    }

    private companion object {
        const val BATCH = 20
    }
}
