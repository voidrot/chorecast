package dev.voidrot.chorecast

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Scaffold
import androidx.compose.ui.Modifier
import dev.voidrot.chorecast.core.SsoCallback
import dev.voidrot.chorecast.ui.ChorecastNavHost
import dev.voidrot.chorecast.ui.theme.ChorecastTheme

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        val container = (application as ChorecastApp).container
        if (savedInstanceState == null) handleRedirect(intent)
        setContent {
            ChorecastTheme {
                Scaffold(modifier = Modifier.fillMaxSize()) { padding ->
                    ChorecastNavHost(container, Modifier.padding(padding))
                }
            }
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
