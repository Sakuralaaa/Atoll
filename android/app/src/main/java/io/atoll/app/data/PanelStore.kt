package io.atoll.app.data

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

data class Panel(
    val id: String = UUID.randomUUID().toString(),
    val name: String,
    val url: String,
    val username: String,
    /** Only set when the user turned on "remember password". */
    val password: String? = null,
    val remember: Boolean = true,
    /** Ask for fingerprint / face / screen lock before opening (only with a saved password). */
    val biometric: Boolean = false,
    val lastUsed: Long = 0,
) {
    fun toJson(): JSONObject = JSONObject()
        .put("id", id).put("name", name).put("url", url).put("username", username)
        .put("password", password ?: JSONObject.NULL).put("remember", remember)
        .put("biometric", biometric).put("lastUsed", lastUsed)

    companion object {
        fun fromJson(o: JSONObject) = Panel(
            id = o.getString("id"),
            name = o.optString("name"),
            url = o.optString("url"),
            username = o.optString("username"),
            password = if (o.isNull("password")) null else o.optString("password"),
            remember = o.optBoolean("remember", true),
            biometric = o.optBoolean("biometric", false),
            lastUsed = o.optLong("lastUsed", 0),
        )
    }
}

/** Saved panels, credentials included, in EncryptedSharedPreferences (key held by the Android Keystore). */
class PanelStore(context: Context) {
    private val prefs: SharedPreferences = open(context)
    private val _panels = MutableStateFlow(load())
    val panels: StateFlow<List<Panel>> = _panels

    fun get(id: String?): Panel? = id?.let { i -> _panels.value.firstOrNull { it.id == i } }

    @Synchronized
    fun upsert(p: Panel) {
        val list = _panels.value.toMutableList()
        val i = list.indexOfFirst { it.id == p.id }
        if (i >= 0) list[i] = p else list.add(p)
        save(list)
    }

    @Synchronized
    fun delete(id: String) = save(_panels.value.filterNot { it.id == id })

    @Synchronized
    fun touch(id: String) {
        val p = get(id) ?: return
        upsert(p.copy(lastUsed = System.currentTimeMillis()))
    }

    private fun save(list: List<Panel>) {
        val arr = JSONArray()
        list.forEach { arr.put(it.toJson()) }
        prefs.edit().putString(KEY, arr.toString()).apply()
        _panels.value = list
    }

    private fun load(): List<Panel> = try {
        val arr = JSONArray(prefs.getString(KEY, "[]") ?: "[]")
        (0 until arr.length()).map { Panel.fromJson(arr.getJSONObject(it)) }
    } catch (e: Exception) {
        emptyList()
    }

    companion object {
        private const val FILE = "atoll_panels"
        private const val KEY = "panels"

        private fun create(context: Context): SharedPreferences {
            val key = MasterKey.Builder(context).setKeyScheme(MasterKey.KeyScheme.AES256_GCM).build()
            return EncryptedSharedPreferences.create(
                context, FILE, key,
                EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
                EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
            )
        }

        private fun open(context: Context): SharedPreferences = try {
            create(context)
        } catch (e: Exception) {
            // The Keystore key was lost (e.g. restored to a new device): start over rather than crash.
            context.deleteSharedPreferences(FILE)
            create(context)
        }
    }
}
