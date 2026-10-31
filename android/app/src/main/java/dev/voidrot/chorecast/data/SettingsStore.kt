package dev.voidrot.chorecast.data

import android.content.SharedPreferences
import androidx.core.content.edit
import dev.voidrot.chorecast.core.Result
import dev.voidrot.chorecast.core.ServerUrl

/** Non-secret app settings. */
class SettingsStore(private val prefs: SharedPreferences) {
    var serverUrl: ServerUrl?
        get() = prefs.getString(KEY_SERVER_URL, null)?.let { (ServerUrl.parse(it) as? Result.Ok)?.value }
        set(value) = prefs.edit { if (value == null) remove(KEY_SERVER_URL) else putString(KEY_SERVER_URL, value.value) }

    private companion object {
        const val KEY_SERVER_URL = "server_url"
    }
}
