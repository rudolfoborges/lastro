package dev.code04.lastro.data

import android.content.Context
import androidx.core.content.edit
import dev.code04.lastro.BuildConfig
import dev.code04.lastro.capture.GOOGLE_WALLET
import java.security.GeneralSecurityException
import java.util.UUID

class Settings(context: Context) {
    private val prefs = context.applicationContext.getSharedPreferences("lastro", Context.MODE_PRIVATE)

    var serverUrl: String
        get() = prefs.getString("server_url", "").orEmpty()
        set(value) = prefs.edit { putString("server_url", value.trim().trimEnd('/')) }

    val deviceId: String
        get() = prefs.getString("device_id", null) ?: ("android-" + UUID.randomUUID().toString().take(8)).also { id ->
            prefs.edit { putString("device_id", id) }
        }

    var sendingEnabled: Boolean
        get() = prefs.getBoolean("sending_enabled", false)
        set(value) = prefs.edit { putBoolean("sending_enabled", value) }

    var paused: Boolean
        get() = prefs.getBoolean("paused", false)
        set(value) = prefs.edit { putBoolean("paused", value) }

    var allowlist: Set<String>
        get() = prefs.getStringSet("allowlist", null) ?: setOf(GOOGLE_WALLET)
        set(value) = prefs.edit { putStringSet("allowlist", value.map(String::trim).filter(String::isNotEmpty).toSet()) }

    val apiKey: String?
        get() = prefs.getString("api_key", null)?.let { sealed ->
            try {
                SecretBox.open(sealed)
            } catch (_: GeneralSecurityException) {
                null
            }
        }

    fun saveApiKey(token: String) = prefs.edit { putString("api_key", SecretBox.seal(token.trim())) }

    fun forgetApiKey() = prefs.edit { remove("api_key") }

    val hasApiKey: Boolean get() = prefs.contains("api_key")

    val hasDestination: Boolean get() = isValidServerUrl(serverUrl) && hasApiKey

    val readyToSend: Boolean get() = sendingEnabled && !paused && hasDestination

    var lastSentAt: Long
        get() = prefs.getLong("last_sent_at", 0)
        set(value) = prefs.edit { putLong("last_sent_at", value) }

    var lastHeartbeatAt: Long
        get() = prefs.getLong("last_heartbeat_at", 0)
        set(value) = prefs.edit { putLong("last_heartbeat_at", value) }

    var serverTracking: String
        get() = prefs.getString("server_tracking", "").orEmpty()
        set(value) = prefs.edit { putString("server_tracking", value) }

    var lastError: String
        get() = prefs.getString("last_error", "").orEmpty()
        set(value) = prefs.edit { putString("last_error", value) }

    companion object {
        fun isValidServerUrl(url: String): Boolean =
            url.startsWith("https://") || (BuildConfig.DEBUG && url.startsWith("http://"))
    }
}
