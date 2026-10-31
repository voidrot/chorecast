package dev.voidrot.chorecast.ui.startup

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.ServerStatus
import dev.voidrot.chorecast.ui.problemText

@Composable
fun StartupScreen(
    viewModel: StartupViewModel,
    onNeedsServer: () -> Unit,
    onReady: (ServerStatus.Ready) -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state) {
        when (val s = state) {
            StartupState.NeedsServer -> onNeedsServer()
            is StartupState.Ready -> onReady(s.status)
            else -> Unit
        }
    }
    Column(
        modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        when (val s = state) {
            is StartupState.Problem -> {
                Text(problemText(s.status), style = MaterialTheme.typography.bodyLarge)
                Button(onClick = viewModel::check) { Text(stringResource(R.string.action_retry)) }
                OutlinedButton(onClick = onNeedsServer) { Text(stringResource(R.string.action_change_server)) }
            }
            else -> {
                CircularProgressIndicator()
                Text(stringResource(R.string.startup_checking))
            }
        }
    }
}
