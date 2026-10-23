package dev.code04.lastro.work

import android.content.Context
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import java.util.concurrent.TimeUnit

object Scheduling {
    private const val DELIVERY = "deliver-notifications"
    private const val HEARTBEAT = "heartbeat"

    private val online = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()

    fun deliverSoon(context: Context) {
        val request = OneTimeWorkRequestBuilder<DeliveryWorker>()
            .setConstraints(online)
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 30, TimeUnit.SECONDS)
            .build()
        WorkManager.getInstance(context).enqueueUniqueWork(DELIVERY, ExistingWorkPolicy.APPEND_OR_REPLACE, request)
    }

    fun keepHeartbeat(context: Context) {
        val request = PeriodicWorkRequestBuilder<HeartbeatWorker>(15, TimeUnit.MINUTES)
            .setConstraints(online)
            .build()
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(HEARTBEAT, ExistingPeriodicWorkPolicy.KEEP, request)
    }

    fun stopHeartbeat(context: Context) {
        WorkManager.getInstance(context).cancelUniqueWork(HEARTBEAT)
    }
}
