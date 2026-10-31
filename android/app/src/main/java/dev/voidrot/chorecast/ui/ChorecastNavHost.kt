package dev.voidrot.chorecast.ui

import android.os.Build
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import dev.voidrot.chorecast.AppContainer
import dev.voidrot.chorecast.BuildConfig
import dev.voidrot.chorecast.ui.connect.ConnectScreen
import dev.voidrot.chorecast.ui.device.DeviceScreen
import dev.voidrot.chorecast.ui.device.DeviceViewModel
import dev.voidrot.chorecast.ui.pair.PairScreen
import dev.voidrot.chorecast.ui.pair.PairViewModel
import dev.voidrot.chorecast.ui.welcome.WelcomeScreen
import dev.voidrot.chorecast.ui.connect.ConnectViewModel
import dev.voidrot.chorecast.ui.home.HomeScreen
import dev.voidrot.chorecast.ui.home.HomeViewModel
import dev.voidrot.chorecast.ui.login.LoginScreen
import dev.voidrot.chorecast.ui.login.LoginViewModel
import dev.voidrot.chorecast.ui.startup.StartupScreen
import dev.voidrot.chorecast.ui.startup.StartupViewModel

/** Shown in the parent's session list, e.g. "Google Pixel 9". */
private val deviceLabel: String
    get() = listOf(Build.MANUFACTURER, Build.MODEL)
        .filter { it.isNotBlank() }
        .distinct()
        .joinToString(" ") { it.replaceFirstChar(Char::uppercase) }
        .ifBlank { "Android" }

/** Navigates to [route], clearing the back stack (startup, sign-in and sign-out are fresh starts). */
private fun NavHostController.reset(route: Any) = navigate(route) { popUpTo(graph.id) { inclusive = true } }

@Composable
fun ChorecastNavHost(container: AppContainer, modifier: Modifier = Modifier) {
    val nav = rememberNavController()
    val currentUrl = { checkNotNull(container.settings.serverUrl) { "no server configured" } }
    NavHost(nav, startDestination = StartupRoute, modifier = modifier) {
        composable<StartupRoute> {
            StartupScreen(
                viewModel = viewModel {
                    StartupViewModel(container.settings, container.serverChecker, container.tokenStore, container.parentAuth)
                },
                onNeedsServer = { nav.reset(WelcomeRoute) },
                onNeedsLogin = { nav.reset(LoginRoute(it.value)) },
                onSignedIn = { nav.reset(HomeRoute(it.value)) },
                onDevice = { nav.reset(DeviceRoute(it.value)) },
            )
        }
        composable<WelcomeRoute> {
            WelcomeScreen(onPair = { nav.navigate(PairRoute) }, onParent = { nav.navigate(ConnectRoute) })
        }
        composable<PairRoute> {
            PairScreen(
                viewModel = viewModel {
                    PairViewModel(container.settings, container.tokenStore, container.deviceClient, deviceLabel, BuildConfig.VERSION_NAME)
                },
                onPaired = { nav.reset(DeviceRoute(currentUrl().value)) },
                onBack = { nav.popBackStack() },
            )
        }
        composable<DeviceRoute> {
            DeviceScreen(
                viewModel = viewModel {
                    DeviceViewModel(currentUrl(), container.deviceClient, container.parentAuth, container.tokenStore)
                },
                onUnpaired = {
                    container.settings.mode = null
                    nav.reset(WelcomeRoute)
                },
            )
        }
        composable<ConnectRoute> {
            ConnectScreen(
                viewModel = viewModel { ConnectViewModel(container.settings, container.serverChecker, container.tokenStore) },
                onConnected = { nav.reset(LoginRoute(it.url.value)) },
            )
        }
        composable<LoginRoute> { entry ->
            val route = entry.toRoute<LoginRoute>()
            LoginScreen(
                viewModel = viewModel {
                    LoginViewModel(
                        currentUrl(), container.parentAuth, container.tokenStore, deviceLabel,
                        container.serverChecker, container.sso,
                    )
                },
                serverUrl = route.serverUrl,
                onSignedIn = { nav.reset(HomeRoute(route.serverUrl, it.recoveryCodesLeft)) },
                onChangeServer = { nav.navigate(ConnectRoute) },
            )
        }
        composable<HomeRoute> { entry ->
            val route = entry.toRoute<HomeRoute>()
            HomeScreen(
                viewModel = viewModel {
                    HomeViewModel(currentUrl(), container.parentAuth, container.tokenStore, container.householdInfo)
                },
                serverUrl = route.serverUrl,
                recoveryCodesLeft = route.recoveryCodesLeft,
                onSignedOut = { nav.reset(LoginRoute(route.serverUrl)) },
                onChangeServer = { nav.navigate(ConnectRoute) },
            )
        }
    }
}
