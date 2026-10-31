package dev.voidrot.chorecast.ui.devices

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

private val lastSeenFormat = DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, FormatStyle.SHORT)

@Composable
fun DevicesScreen(viewModel: DevicesViewModel, onBack: () -> Unit, onSignedOut: () -> Unit, modifier: Modifier = Modifier) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    LaunchedEffect(state.signedOut) { if (state.signedOut) onSignedOut() }
    LaunchedEffect(state.notice) {
        val n = state.notice ?: return@LaunchedEffect
        val text = when (n) {
            is Notice.Locked -> context.getString(R.string.devices_locked, n.name)
            is Notice.Revoked -> context.getString(R.string.devices_revoked, n.name)
            Notice.Failed -> context.getString(R.string.devices_failed)
        }
        android.widget.Toast.makeText(context, text, android.widget.Toast.LENGTH_LONG).show()
        viewModel.noticeShown()
    }

    Column(modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
            Text(stringResource(R.string.devices_title), style = MaterialTheme.typography.headlineSmall)
            TextButton(onClick = onBack) { Text(stringResource(R.string.action_back)) }
        }
        when {
            state.loading && state.devices.isEmpty() -> CircularProgressIndicator()
            state.error != null -> {
                Text(stringResource(R.string.device_offline, state.error.orEmpty()), color = MaterialTheme.colorScheme.error)
                Button(onClick = viewModel::load) { Text(stringResource(R.string.action_retry)) }
            }
            state.devices.isEmpty() -> Text(stringResource(R.string.devices_none))
            else -> LazyColumn(Modifier.weight(1f)) {
                items(state.devices, key = { it.id }) { d ->
                    Column(Modifier.fillMaxWidth().padding(vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text(d.name, style = MaterialTheme.typography.titleMedium)
                        Text(
                            if (d.shared) stringResource(R.string.devices_shared) else stringResource(R.string.devices_for_kid, d.kidName.orEmpty()),
                            style = MaterialTheme.typography.bodyMedium,
                        )
                        d.sessionUser?.takeIf { it.isNotEmpty() }?.let {
                            Text(stringResource(R.string.devices_in_use, it), color = MaterialTheme.colorScheme.primary)
                        }
                        Text(
                            stringResource(R.string.devices_last_seen, d.lastSeen.atZoneSameInstant(ZoneId.systemDefault()).format(lastSeenFormat)) +
                                (d.appVersion?.takeIf { it.isNotEmpty() }?.let { " · " + stringResource(R.string.devices_version, it) } ?: ""),
                            style = MaterialTheme.typography.bodySmall,
                        )
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            if (d.shared) {
                                OutlinedButton(onClick = { viewModel.lock(d) }, enabled = !d.sessionUser.isNullOrEmpty()) {
                                    Text(stringResource(R.string.device_lock))
                                }
                            }
                            TextButton(onClick = { viewModel.askRevoke(d) }) {
                                Text(stringResource(R.string.devices_revoke), color = MaterialTheme.colorScheme.error)
                            }
                        }
                    }
                    HorizontalDivider()
                }
            }
        }
        Text(stringResource(R.string.devices_hint), style = MaterialTheme.typography.bodySmall)
    }

    state.confirmRevoke?.let { d ->
        AlertDialog(
            onDismissRequest = { viewModel.askRevoke(null) },
            title = { Text(stringResource(R.string.devices_revoke)) },
            text = { Text(stringResource(R.string.devices_revoke_confirm, d.name)) },
            confirmButton = { Button(onClick = viewModel::revoke) { Text(stringResource(R.string.devices_revoke)) } },
            dismissButton = { TextButton(onClick = { viewModel.askRevoke(null) }) { Text(stringResource(R.string.action_cancel)) } },
        )
    }
}
