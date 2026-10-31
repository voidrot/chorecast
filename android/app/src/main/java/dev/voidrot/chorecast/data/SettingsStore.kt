package dev.voidrot.chorecast.data

import android.content.SharedPreferences
import androidx.core.content.edit
import dev.voidrot.chorecast.core.Result
import dev.voidrot.chorecast.core.ServerUrl

/** How this install is used: a parent's phone, or a paired kid/shared device. */
enum class AppMode { PARENT, DEVICE }

/** Non-secret app settings. */
class SettingsStore(private val prefs: SharedPreferences) {
    /** Null until the user picks; installs from before pairing existed count as PARENT once a server is set. */
    var mode: AppMode?
        get() = prefs.getString(KEY_MODE, null)?.let { runCatching { AppMode.valueOf(it) }.getOrNull() }
            ?: if (serverUrl != null) AppMode.PARENT else null
        set(value) = prefs.edit { if (value == null) remove(KEY_MODE) else putString(KEY_MODE, value.name) }

    var serverUrl: ServerUrl?
        get() = prefs.getString(KEY_SERVER_URL, null)?.let { (ServerUrl.parse(it) as? Result.Ok)?.value }
        set(value) = prefs.edit { if (value == null) remove(KEY_SERVER_URL) else putString(KEY_SERVER_URL, value.value) }

    private companion object {
        const val KEY_SERVER_URL = "server_url"
        const val KEY_MODE = "mode"
    }
}
