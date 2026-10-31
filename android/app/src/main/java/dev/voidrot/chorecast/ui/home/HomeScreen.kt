package dev.voidrot.chorecast.ui.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R

/** Placeholder parent home until the parent screens (#32) and device pairing (#18) exist. */
@Composable
fun HomeScreen(
    viewModel: HomeViewModel,
    serverUrl: String,
    recoveryCodesLeft: Long?,
    onSignedOut: () -> Unit,
    onChangeServer: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.signedOut) { if (state.signedOut) onSignedOut() }

    Column(
        modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        state.displayName?.let {
            Text(stringResource(R.string.home_signed_in, it), style = MaterialTheme.typography.headlineSmall)
        }
        state.household?.let {
            Text(it.name, style = MaterialTheme.typography.titleMedium)
            Text(stringResource(R.string.home_household, it.timezone, it.dayEnd), style = MaterialTheme.typography.bodyMedium)
        }
        Text(stringResource(R.string.home_server, serverUrl), style = MaterialTheme.typography.bodyMedium)
        if (recoveryCodesLeft != null && recoveryCodesLeft <= 3) {
            Text(
                pluralStringResource(R.plurals.home_recovery_codes_left, recoveryCodesLeft.toInt(), recoveryCodesLeft.toInt()),
                color = MaterialTheme.colorScheme.error,
            )
        }
        Text(stringResource(R.string.home_parent_next), style = MaterialTheme.typography.bodyMedium)
        OutlinedButton(onClick = viewModel::signOut) { Text(stringResource(R.string.action_sign_out)) }
        OutlinedButton(onClick = onChangeServer) { Text(stringResource(R.string.action_change_server)) }
    }
}
