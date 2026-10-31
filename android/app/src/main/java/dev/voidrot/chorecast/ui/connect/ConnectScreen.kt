package dev.voidrot.chorecast.ui.connect

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.ServerStatus
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.ui.problemText

@Composable
fun ConnectScreen(
    viewModel: ConnectViewModel,
    onConnected: (ServerStatus.Ready) -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.connected) { state.connected?.let(onConnected) }
    val inputError = state.inputError

    Column(modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Text(stringResource(R.string.connect_title), style = MaterialTheme.typography.headlineSmall)
        Text(stringResource(R.string.connect_explainer), style = MaterialTheme.typography.bodyMedium)
        OutlinedTextField(
            value = state.input,
            onValueChange = viewModel::onInput,
            label = { Text(stringResource(R.string.connect_url_label)) },
            placeholder = { Text(stringResource(R.string.connect_url_placeholder)) },
            singleLine = true,
            isError = inputError != null,
            supportingText = if (inputError != null) {
                { Text(inputErrorText(inputError)) }
            } else {
                null
            },
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go),
            keyboardActions = KeyboardActions(onGo = { viewModel.connect() }),
            modifier = Modifier.fillMaxWidth(),
        )
        if (state.cleartext) {
            Text(stringResource(R.string.connect_cleartext_warning), color = MaterialTheme.colorScheme.error)
        }
        state.problem?.let { Text(problemText(it), color = MaterialTheme.colorScheme.error) }
        Button(onClick = viewModel::connect, enabled = !state.checking, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(if (state.checking) R.string.connect_checking else R.string.action_connect))
        }
    }
}

@Composable
private fun inputErrorText(error: ServerUrl.Error): String = when (error) {
    ServerUrl.Error.Empty -> stringResource(R.string.connect_error_empty)
    ServerUrl.Error.UnsupportedScheme -> stringResource(R.string.connect_error_scheme)
    ServerUrl.Error.Invalid -> stringResource(R.string.connect_error_invalid)
}
