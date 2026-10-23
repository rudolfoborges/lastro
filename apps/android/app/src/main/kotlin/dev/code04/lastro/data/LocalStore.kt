package dev.code04.lastro.data

import android.content.ContentValues
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import dev.code04.lastro.capture.CapturedNotification
import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant

data class OutboxItem(val id: Long, val body: String)

class LocalStore private constructor(context: Context) : SQLiteOpenHelper(context, "lastro.db", null, 1) {
    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL("CREATE TABLE outbox (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL, created_at INTEGER NOT NULL)")
        db.execSQL("CREATE TABLE seen (notification_key TEXT PRIMARY KEY, content_hash TEXT NOT NULL)")
        db.execSQL(
            "CREATE TABLE captures (id INTEGER PRIMARY KEY AUTOINCREMENT, package TEXT NOT NULL, notification_key TEXT NOT NULL, " +
                "event TEXT NOT NULL, posted_at INTEGER NOT NULL, title TEXT NOT NULL, text TEXT NOT NULL, big_text TEXT NOT NULL, " +
                "captured_at INTEGER NOT NULL)",
        )
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) = Unit

    fun seenHash(key: String): String? =
        readableDatabase.rawQuery("SELECT content_hash FROM seen WHERE notification_key = ?", arrayOf(key)).use { cursor ->
            if (cursor.moveToFirst()) cursor.getString(0) else null
        }

    fun record(captured: CapturedNotification, outboxBody: String?) {
        writableDatabase.transaction {
            insertWithOnConflict(
                "seen",
                null,
                ContentValues().apply {
                    put("notification_key", captured.key)
                    put("content_hash", captured.contentHash)
                },
                SQLiteDatabase.CONFLICT_REPLACE,
            )
            insert(
                "captures",
                null,
                ContentValues().apply {
                    put("package", captured.packageName)
                    put("notification_key", captured.key)
                    put("event", captured.event.wire)
                    put("posted_at", captured.postTime)
                    put("title", captured.title)
                    put("text", captured.text)
                    put("big_text", captured.bigText)
                    put("captured_at", System.currentTimeMillis())
                },
            )
            execSQL("DELETE FROM captures WHERE id NOT IN (SELECT id FROM captures ORDER BY id DESC LIMIT $MAX_CAPTURES)")
            if (outboxBody != null) {
                insert("outbox", null, ContentValues().apply {
                    put("body", outboxBody)
                    put("created_at", System.currentTimeMillis())
                })
            }
        }
    }

    fun pending(limit: Int): List<OutboxItem> =
        readableDatabase.rawQuery("SELECT id, body FROM outbox ORDER BY id LIMIT ?", arrayOf(limit.toString())).use { cursor ->
            buildList {
                while (cursor.moveToNext()) add(OutboxItem(cursor.getLong(0), cursor.getString(1)))
            }
        }

    fun delivered(id: Long) {
        writableDatabase.delete("outbox", "id = ?", arrayOf(id.toString()))
    }

    fun queueSize(): Int = count("outbox")

    fun captureCount(): Int = count("captures")

    fun capturesAsJson(): String {
        val items = JSONArray()
        readableDatabase.rawQuery(
            "SELECT package, notification_key, event, posted_at, title, text, big_text, captured_at FROM captures ORDER BY id",
            null,
        ).use { cursor ->
            while (cursor.moveToNext()) {
                items.put(JSONObject().apply {
                    put("package", cursor.getString(0))
                    put("notification_key", cursor.getString(1))
                    put("event", cursor.getString(2))
                    put("posted_at", Instant.ofEpochMilli(cursor.getLong(3)).toString())
                    put("title", cursor.getString(4))
                    put("text", cursor.getString(5))
                    put("big_text", cursor.getString(6))
                    put("captured_at", Instant.ofEpochMilli(cursor.getLong(7)).toString())
                })
            }
        }
        return items.toString(2)
    }

    fun clearCaptures() {
        writableDatabase.delete("captures", null, null)
    }

    private fun count(table: String): Int =
        readableDatabase.rawQuery("SELECT count(*) FROM $table", null).use { cursor ->
            if (cursor.moveToFirst()) cursor.getInt(0) else 0
        }

    private inline fun SQLiteDatabase.transaction(block: SQLiteDatabase.() -> Unit) {
        beginTransaction()
        try {
            block()
            setTransactionSuccessful()
        } finally {
            endTransaction()
        }
    }

    companion object {
        private const val MAX_CAPTURES = 500

        @Volatile
        private var instance: LocalStore? = null

        fun get(context: Context): LocalStore =
            instance ?: synchronized(this) {
                instance ?: LocalStore(context.applicationContext).also { instance = it }
            }
    }
}
