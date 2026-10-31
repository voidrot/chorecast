package dev.voidrot.chorecast.ui.home

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import dev.voidrot.chorecast.R

/** Placeholder until device pairing (#18) and the kid and parent screens exist. */
@Composable
fun HomeScreen(serverUrl: String, serverVersion: String, onChangeServer: () -> Unit, modifier: Modifier = Modifier) {
    Column(
        modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(stringResource(R.string.home_connected, serverUrl, serverVersion), style = MaterialTheme.typography.bodyLarge)
        Text(stringResource(R.string.home_pairing_next), style = MaterialTheme.typography.bodyMedium)
        OutlinedButton(onClick = onChangeServer) { Text(stringResource(R.string.action_change_server)) }
    }
}
