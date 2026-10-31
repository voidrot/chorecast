package dev.voidrot.chorecast.ui.login

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R

@Composable
fun LoginScreen(
    viewModel: LoginViewModel,
    serverUrl: String,
    onSignedIn: (LoginState.SignedIn) -> Unit,
    onChangeServer: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.signedIn) { state.signedIn?.let(onSignedIn) }

    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(stringResource(R.string.login_title), style = MaterialTheme.typography.headlineSmall)
        Text(stringResource(R.string.login_server, serverUrl), style = MaterialTheme.typography.bodyMedium)

        if (state.step == LoginStep.Password) {
            OutlinedTextField(
                value = state.username,
                onValueChange = viewModel::onUsername,
                label = { Text(stringResource(R.string.login_username)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(
                    capitalization = KeyboardCapitalization.None,
                    autoCorrectEnabled = false,
                    keyboardType = KeyboardType.Email,
                    imeAction = ImeAction.Next,
                ),
                modifier = Modifier.fillMaxWidth(),
            )
            OutlinedTextField(
                value = state.password,
                onValueChange = viewModel::onPassword,
                label = { Text(stringResource(R.string.login_password)) },
                singleLine = true,
                visualTransformation = PasswordVisualTransformation(),
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Go),
                keyboardActions = KeyboardActions(onGo = { viewModel.submit() }),
                modifier = Modifier.fillMaxWidth(),
            )
        } else {
            val recovery = state.step == LoginStep.Recovery
            Text(stringResource(if (recovery) R.string.login_recovery_prompt else R.string.login_code_prompt))
            OutlinedTextField(
                value = state.code,
                onValueChange = viewModel::onCode,
                label = { Text(stringResource(if (recovery) R.string.login_recovery_code else R.string.login_code)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(
                    keyboardType = if (recovery) KeyboardType.Ascii else KeyboardType.NumberPassword,
                    autoCorrectEnabled = false,
                    imeAction = ImeAction.Go,
                ),
                keyboardActions = KeyboardActions(onGo = { viewModel.submit() }),
                modifier = Modifier.fillMaxWidth(),
            )
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                TextButton(onClick = { viewModel.useRecoveryCode(!recovery) }) {
                    Text(stringResource(if (recovery) R.string.login_use_code else R.string.login_use_recovery))
                }
                TextButton(onClick = viewModel::startOver) { Text(stringResource(R.string.login_start_over)) }
            }
        }

        state.error?.let { Text(errorText(it, state.step), color = MaterialTheme.colorScheme.error) }

        Button(onClick = viewModel::submit, enabled = state.canSubmit, modifier = Modifier.fillMaxWidth()) {
            Text(
                stringResource(
                    when {
                        state.submitting -> R.string.login_signing_in
                        state.step == LoginStep.Password -> R.string.action_sign_in
                        else -> R.string.action_verify
                    },
                ),
            )
        }
        TextButton(onClick = onChangeServer) { Text(stringResource(R.string.action_change_server)) }
    }
}

@Composable
private fun errorText(error: LoginError, step: LoginStep): String = when (error) {
    LoginError.InvalidCredentials -> stringResource(R.string.login_error_credentials)
    LoginError.InvalidCode ->
        stringResource(if (step == LoginStep.Recovery) R.string.login_error_recovery else R.string.login_error_code)
    is LoginError.RateLimited ->
        pluralStringResource(R.plurals.login_error_rate_limited, error.minutes.toInt(), error.minutes.toInt())
    is LoginError.Unreachable -> stringResource(R.string.login_error_unreachable, error.reason)
    is LoginError.Failed -> stringResource(R.string.login_error_failed, error.code)
}
