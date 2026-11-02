package dev.voidrot.chorecast

import android.content.Context
import dev.voidrot.chorecast.core.ChoreCompleter
import dev.voidrot.chorecast.core.DeviceClient
import dev.voidrot.chorecast.core.HouseholdInfo
import dev.voidrot.chorecast.core.KidChores
import dev.voidrot.chorecast.core.ParentAuth
import dev.voidrot.chorecast.core.ServerChecker
import dev.voidrot.chorecast.core.TokenStore
import dev.voidrot.chorecast.data.FileCaptureStore
import dev.voidrot.chorecast.data.KeystoreTokenStore
import dev.voidrot.chorecast.data.SettingsStore
import dev.voidrot.chorecast.data.SsoBridge
import io.ktor.client.engine.okhttp.OkHttp
import java.io.File

/**
 * Manual dependency injection: the app's long-lived objects, created once in [ChorecastApp].
 * ViewModels receive what they need from here.
 */
class AppContainer(context: Context) {
    private val prefs = context.getSharedPreferences("chorecast", Context.MODE_PRIVATE)

    val settings = SettingsStore(prefs)
    val tokenStore: TokenStore = KeystoreTokenStore(prefs)
    private val engine = OkHttp.create()
    val serverChecker = ServerChecker(engine)
    val parentAuth = ParentAuth(engine)
    val householdInfo = HouseholdInfo(engine)
    val deviceClient = DeviceClient(engine)
    val kidChores = KidChores(engine)
    val sso = SsoBridge()
    val captures = FileCaptureStore(File(context.filesDir, "captures"))

    /** One per kid session: it holds the idempotency key of the submit in progress. */
    fun choreCompleter() = ChoreCompleter(captures, engine = engine)
}
