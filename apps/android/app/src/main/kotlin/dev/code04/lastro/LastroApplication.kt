package dev.code04.lastro

import android.app.Application
import dev.code04.lastro.data.Settings
import dev.code04.lastro.work.Scheduling

class LastroApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        if (Settings(this).readyToSend) Scheduling.keepHeartbeat(this)
    }
}
