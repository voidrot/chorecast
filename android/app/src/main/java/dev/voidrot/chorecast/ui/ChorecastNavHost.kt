package dev.voidrot.chorecast.ui

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import dev.voidrot.chorecast.AppContainer
import dev.voidrot.chorecast.core.ServerStatus
import dev.voidrot.chorecast.ui.connect.ConnectScreen
import dev.voidrot.chorecast.ui.connect.ConnectViewModel
import dev.voidrot.chorecast.ui.home.HomeScreen
import dev.voidrot.chorecast.ui.startup.StartupScreen
import dev.voidrot.chorecast.ui.startup.StartupViewModel

@Composable
fun ChorecastNavHost(container: AppContainer, modifier: Modifier = Modifier) {
    val nav = rememberNavController()
    val toHome = { status: ServerStatus.Ready ->
        nav.navigate(HomeRoute(status.url.value, status.serverVersion)) {
            popUpTo(nav.graph.id) { inclusive = true }
        }
    }
    NavHost(nav, startDestination = StartupRoute, modifier = modifier) {
        composable<StartupRoute> {
            StartupScreen(
                viewModel = viewModel { StartupViewModel(container.settings, container.serverChecker) },
                onNeedsServer = { nav.navigate(ConnectRoute) { popUpTo(nav.graph.id) { inclusive = true } } },
                onReady = toHome,
            )
        }
        composable<ConnectRoute> {
            ConnectScreen(
                viewModel = viewModel { ConnectViewModel(container.settings, container.serverChecker) },
                onConnected = toHome,
            )
        }
        composable<HomeRoute> { entry ->
            val route = entry.toRoute<HomeRoute>()
            HomeScreen(route.serverUrl, route.serverVersion, onChangeServer = { nav.navigate(ConnectRoute) })
        }
    }
}
