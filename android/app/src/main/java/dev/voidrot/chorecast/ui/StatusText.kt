package dev.voidrot.chorecast.ui

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.Compatibility
import dev.voidrot.chorecast.core.ServerStatus

/** A user-facing explanation of why the app can't use a server. */
@Composable
fun problemText(status: ServerStatus): String = when (status) {
    is ServerStatus.Ready -> ""
    is ServerStatus.Unreachable -> stringResource(R.string.server_unreachable, status.url.value, status.reason)
    is ServerStatus.NotChorecast -> stringResource(R.string.server_not_chorecast, status.url.value)
    is ServerStatus.Incompatible -> when (val c = status.compatibility) {
        is Compatibility.AppTooOld -> stringResource(R.string.server_app_too_old, status.serverVersion)
        is Compatibility.ServerTooOld -> stringResource(R.string.server_too_old, status.serverVersion)
        Compatibility.Compatible -> ""
    }
}
