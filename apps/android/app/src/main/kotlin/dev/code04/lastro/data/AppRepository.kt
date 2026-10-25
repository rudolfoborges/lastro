package dev.code04.lastro.data

import android.content.Context
import androidx.core.content.edit
import dev.code04.lastro.net.Analysis
import dev.code04.lastro.net.AppJson
import dev.code04.lastro.net.ExpenseDetail
import dev.code04.lastro.net.ExpenseList
import dev.code04.lastro.net.ExpenseQuery
import dev.code04.lastro.net.LastroClient
import dev.code04.lastro.net.Overview
import dev.code04.lastro.net.Whoami
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.net.URLEncoder

enum class ProblemKind { NOT_CONFIGURED, NO_READ_ACCESS, UNAUTHORIZED, NOT_FOUND, NETWORK, SERVER }

data class Problem(val kind: ProblemKind, val message: String)

sealed interface Fetch<out T> {
    data class Ok<T>(val value: T) : Fetch<T>

    data class Failed(val problem: Problem) : Fetch<Nothing>
}

data class Saved<T>(val value: T, val savedAt: Long)

class AppRepository(context: Context) {
    private val context = context.applicationContext
    private val cache = context.getSharedPreferences("lastro_cache", Context.MODE_PRIVATE)

    val configured: Boolean get() = Settings(context).hasDestination

    suspend fun overview(month: String?): Fetch<Overview> {
        val path = "/v1/overview?currency=BRL" + month?.let { "&month=${encode(it)}" }.orEmpty()
        return when (val raw = getRaw(path)) {
            is Fetch.Failed -> raw
            is Fetch.Ok -> {
                val overview = AppJson.overview(raw.value)
                if (overview.period.inProgress) {
                    cache.edit {
                        putString(OVERVIEW, raw.value.toString())
                        putLong(OVERVIEW_SAVED_AT, System.currentTimeMillis())
                    }
                }
                Fetch.Ok(overview)
            }
        }
    }

    fun savedOverview(): Saved<Overview>? {
        val body = cache.getString(OVERVIEW, null) ?: return null
        return try {
            Saved(AppJson.overview(JSONObject(body)), cache.getLong(OVERVIEW_SAVED_AT, 0))
        } catch (_: JSONException) {
            null
        }
    }

    fun forgetSaved() = cache.edit { clear() }

    suspend fun expenses(query: ExpenseQuery, offset: Int): Fetch<ExpenseList> = get(query.path(offset), AppJson::expenses)

    suspend fun expense(id: String): Fetch<ExpenseDetail> = get("/v1/expenses/${encode(id)}", AppJson::detail)

    suspend fun analysis(months: Int): Fetch<Analysis> = get("/v1/analysis?currency=BRL&months=$months", AppJson::analysis)

    suspend fun whoami(): Fetch<Whoami> = get("/v1/whoami", AppJson::whoami)

    private suspend fun <T> get(path: String, parse: (JSONObject) -> T): Fetch<T> = when (val raw = getRaw(path)) {
        is Fetch.Failed -> raw
        is Fetch.Ok -> Fetch.Ok(parse(raw.value))
    }

    private suspend fun getRaw(path: String): Fetch<JSONObject> = withContext(Dispatchers.IO) {
        val settings = Settings(context)
        val apiKey = settings.apiKey
        if (!settings.hasDestination || apiKey == null) {
            return@withContext Fetch.Failed(Problem(ProblemKind.NOT_CONFIGURED, "Configure o servidor e a credencial em Configurações."))
        }
        val response = try {
            LastroClient(settings.serverUrl, apiKey).get(path)
        } catch (_: IOException) {
            return@withContext Fetch.Failed(Problem(ProblemKind.NETWORK, "Sem conexão com o Lastro."))
        }
        val body = response.body
        when {
            response.status == 401 -> Fetch.Failed(Problem(ProblemKind.UNAUTHORIZED, "A credencial guardada foi recusada. Substitua em Configurações."))
            response.status == 403 -> Fetch.Failed(
                Problem(ProblemKind.NO_READ_ACCESS, "A credencial guardada não permite consultar despesas. Use uma com expenses:read e wallet:ingest."),
            )
            response.status == 404 -> Fetch.Failed(Problem(ProblemKind.NOT_FOUND, "Registro não encontrado."))
            response.status !in 200..299 || body == null -> Fetch.Failed(Problem(ProblemKind.SERVER, "O Lastro não respondeu como esperado (${response.status})."))
            else -> Fetch.Ok(body)
        }
    }

    private companion object {
        const val OVERVIEW = "overview"
        const val OVERVIEW_SAVED_AT = "overview_saved_at"

        fun encode(value: String): String = URLEncoder.encode(value, Charsets.UTF_8)
    }
}
