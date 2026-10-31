package dev.voidrot.chorecast.ui.pair

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R

@Composable
fun PairScreen(viewModel: PairViewModel, onPaired: () -> Unit, onBack: () -> Unit, modifier: Modifier = Modifier) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    LaunchedEffect(state.paired) { if (state.paired) onPaired() }

    var hasCamera by remember {
        mutableStateOf(ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED)
    }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        hasCamera = granted
        if (!granted) viewModel.showManual(true)
    }
    LaunchedEffect(Unit) { if (!hasCamera && !state.manual) permission.launch(Manifest.permission.CAMERA) }

    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(stringResource(R.string.pair_title), style = MaterialTheme.typography.headlineSmall)
        if (!state.manual) {
            Text(stringResource(R.string.pair_scan_hint), style = MaterialTheme.typography.bodyMedium)
            Box(Modifier.fillMaxWidth().aspectRatio(1f), contentAlignment = Alignment.Center) {
                if (hasCamera && !state.pairing) {
                    key(state.scanAttempt) {
                        QrScanner(onScanned = viewModel::onScanned, modifier = Modifier.fillMaxSize())
                    }
                } else if (state.pairing) {
                    CircularProgressIndicator()
                }
            }
            TextButton(onClick = { viewModel.showManual(true) }) { Text(stringResource(R.string.pair_type_instead)) }
        } else {
            Text(stringResource(R.string.pair_manual_hint), style = MaterialTheme.typography.bodyMedium)
            OutlinedTextField(
                value = state.server,
                onValueChange = viewModel::onServer,
                label = { Text(stringResource(R.string.connect_url_label)) },
                placeholder = { Text(stringResource(R.string.connect_url_placeholder)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Next),
                modifier = Modifier.fillMaxWidth(),
            )
            OutlinedTextField(
                value = state.code,
                onValueChange = viewModel::onCode,
                label = { Text(stringResource(R.string.pair_code)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(
                    capitalization = KeyboardCapitalization.None,
                    autoCorrectEnabled = false,
                    keyboardType = KeyboardType.Ascii,
                    imeAction = ImeAction.Go,
                ),
                keyboardActions = KeyboardActions(onGo = { viewModel.submitManual() }),
                modifier = Modifier.fillMaxWidth(),
            )
            Button(onClick = viewModel::submitManual, enabled = !state.pairing, modifier = Modifier.fillMaxWidth()) {
                Text(stringResource(if (state.pairing) R.string.pair_pairing else R.string.action_pair))
            }
            TextButton(onClick = {
                viewModel.showManual(false)
                if (!hasCamera) permission.launch(Manifest.permission.CAMERA)
            }) { Text(stringResource(R.string.pair_scan_instead)) }
        }
        state.error?.let { Text(pairErrorText(it), color = MaterialTheme.colorScheme.error) }
        TextButton(onClick = onBack) { Text(stringResource(R.string.action_back)) }
    }
}

@Composable
private fun pairErrorText(e: PairError): String = when (e) {
    PairError.NotAPairingCode -> stringResource(R.string.pair_error_not_code)
    PairError.InvalidServer -> stringResource(R.string.connect_error_invalid)
    PairError.InvalidCode -> stringResource(R.string.pair_error_invalid)
    PairError.RateLimited -> stringResource(R.string.pair_error_rate_limited)
    is PairError.Unreachable -> stringResource(R.string.login_error_unreachable, e.reason)
    is PairError.Failed -> stringResource(R.string.login_error_failed, e.code)
}
