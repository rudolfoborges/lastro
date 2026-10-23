package dev.code04.lastro.ui

import android.content.Context
import android.os.PowerManager
import androidx.core.app.NotificationManagerCompat

object DeviceChecks {
    fun hasNotificationAccess(context: Context): Boolean =
        context.packageName in NotificationManagerCompat.getEnabledListenerPackages(context)

    fun isBatteryUnrestricted(context: Context): Boolean =
        context.getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(context.packageName)
}
