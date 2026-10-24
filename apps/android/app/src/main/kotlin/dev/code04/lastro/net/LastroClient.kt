package dev.code04.lastro.net

import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URI

class LastroClient(private val baseUrl: String, private val apiKey: String) {
    data class Response(val status: Int, val body: JSONObject?) {
        val errorCode: String? get() = body?.optString("error")?.takeIf { it.isNotEmpty() }
    }

    @Throws(IOException::class)
    fun post(path: String, body: JSONObject): Response = request("POST", path, body)

    @Throws(IOException::class)
    fun get(path: String): Response = request("GET", path, null)

    private fun request(method: String, path: String, body: JSONObject?): Response {
        val connection = URI(baseUrl.trimEnd('/') + path).toURL().openConnection() as HttpURLConnection
        try {
            connection.requestMethod = method
            connection.connectTimeout = TIMEOUT_MS
            connection.readTimeout = TIMEOUT_MS
            connection.setRequestProperty("Authorization", "Bearer $apiKey")
            if (body != null) {
                connection.doOutput = true
                connection.setRequestProperty("Content-Type", "application/json")
                connection.outputStream.use { it.write(body.toString().toByteArray()) }
            }
            val status = connection.responseCode
            val stream = if (status >= 400) connection.errorStream else connection.inputStream
            val text = stream?.bufferedReader()?.use { it.readText() }.orEmpty()
            return Response(status, parse(text))
        } finally {
            connection.disconnect()
        }
    }

    private fun parse(text: String): JSONObject? = try {
        if (text.isBlank()) null else JSONObject(text)
    } catch (_: JSONException) {
        null
    }

    private companion object {
        const val TIMEOUT_MS = 15_000
    }
}
