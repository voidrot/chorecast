package dev.voidrot.chorecast

import android.Manifest
import android.content.Intent
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.SystemBarStyle
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Scaffold
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.core.SsoCallback
import dev.voidrot.chorecast.data.AppMode
import dev.voidrot.chorecast.reminders.ReminderNotifier
import dev.voidrot.chorecast.ui.ChorecastNavHost
import dev.voidrot.chorecast.ui.theme.ChorecastTheme

// androidx's own default navigation bar scrims.
private val LIGHT_SCRIM = Color.argb(0xe6, 0xFF, 0xFF, 0xFF)
private val DARK_SCRIM = Color.argb(0x80, 0x1b, 0x1b, 0x1b)

class MainActivity : ComponentActivity() {
    private val askNotifications = registerForActivityResult(ActivityResultContracts.RequestPermission()) {}

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        val container = (application as ChorecastApp).container
        if (savedInstanceState == null) handleRedirect(intent)
        setContent {
            val appearance by container.appearance.state.collectAsStateWithLifecycle()
            val dark = appearance.darkTheme(isSystemInDarkTheme())
            // Bar icons follow the chosen theme, which can differ from the system's.
            DisposableEffect(dark) {
                enableEdgeToEdge(
                    statusBarStyle = SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT) { dark },
                    navigationBarStyle = SystemBarStyle.auto(LIGHT_SCRIM, DARK_SCRIM) { dark },
                )
                onDispose {}
            }
            ChorecastTheme(darkTheme = dark, dynamicColor = appearance.dynamicColor) {
                Scaffold(modifier = Modifier.fillMaxSize()) { padding ->
                    ChorecastNavHost(container, Modifier.padding(padding))
                }
            }
        }
    }

    /** The app opened: refresh the reminders' data, and make sure the periodic sync is scheduled. */
    override fun onStart() {
        super.onStart()
        val container = (application as ChorecastApp).container
        container.reminderScheduler.schedulePeriodicSync()
        container.reminderScheduler.syncNow()
        // Reminders are for paired devices; ask once, on Android 13+.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && container.settings.mode == AppMode.DEVICE &&
            !container.settings.askedNotifications && !ReminderNotifier.canNotify(this)
        ) {
            container.settings.askedNotifications = true
            askNotifications.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleRedirect(intent)
    }

    /** The single sign-on redirect (chorecast://auth?code=… or ?error=…). */
    private fun handleRedirect(intent: Intent?) {
        val data = intent?.data?.toString() ?: return
        SsoCallback.parse(data)?.let { (application as ChorecastApp).container.sso.deliver(it) }
    }
}
