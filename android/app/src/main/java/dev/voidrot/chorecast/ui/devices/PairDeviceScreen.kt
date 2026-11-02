package dev.voidrot.chorecast.ui.devices

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.IssuedPairing
import dev.voidrot.chorecast.core.QrMatrix
import dev.voidrot.chorecast.core.formatCountdown
import dev.voidrot.chorecast.core.secondsUntil
import kotlinx.coroutines.delay
import java.time.Instant

@Composable
fun PairDeviceScreen(viewModel: PairDeviceViewModel, onBack: () -> Unit, onSignedOut: () -> Unit, modifier: Modifier = Modifier) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.signedOut) { if (state.signedOut) onSignedOut() }

    Column(modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
            Text(stringResource(R.string.pair_device_title), style = MaterialTheme.typography.headlineSmall)
            TextButton(onClick = onBack) { Text(stringResource(R.string.action_back)) }
        }
        val pairing = state.pairing
        if (pairing != null) {
            CodeView(pairing, creating = state.creating, onRefresh = viewModel::create, onDone = viewModel::reset)
        } else {
            ChooseView(state, viewModel)
        }
        state.error?.let {
            Text(stringResource(R.string.pair_device_failed, it), color = MaterialTheme.colorScheme.error)
        }
    }
}

@Composable
private fun ColumnScope.ChooseView(state: PairDeviceState, viewModel: PairDeviceViewModel) {
    if (state.loadingKids) {
        CircularProgressIndicator()
        return
    }
    Text(stringResource(R.string.pair_device_choose))
    LazyColumn(Modifier.weight(1f)) {
        item { ChoiceRow(stringResource(R.string.pair_device_shared), state.kidId == null) { viewModel.choose(null) } }
        items(state.kids, key = { it.id }) { kid ->
            ChoiceRow(stringResource(R.string.pair_device_for_kid, kid.displayName), state.kidId == kid.id) { viewModel.choose(kid.id) }
        }
    }
    if (state.error != null && state.kids.isEmpty()) {
        OutlinedButton(onClick = viewModel::loadKids) { Text(stringResource(R.string.action_retry)) }
    }
    Button(onClick = viewModel::create, enabled = !state.creating, modifier = Modifier.fillMaxWidth()) {
        Text(stringResource(R.string.pair_device_show_code))
    }
}

@Composable
private fun ChoiceRow(label: String, selected: Boolean, onSelect: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().selectable(selected = selected, onClick = onSelect, role = Role.RadioButton).padding(vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RadioButton(selected = selected, onClick = null)
        Text(label, Modifier.padding(start = 12.dp))
    }
}

@Composable
private fun CodeView(pairing: IssuedPairing, creating: Boolean, onRefresh: () -> Unit, onDone: () -> Unit) {
    // Ticks once a second until the code expires, then stops.
    var now by remember(pairing) { mutableStateOf(Instant.now()) }
    LaunchedEffect(pairing) {
        while (secondsUntil(pairing.expiresAt, now) > 0) {
            delay(1000)
            now = Instant.now()
        }
    }
    val left = secondsUntil(pairing.expiresAt, now)

    Column(
        Modifier.fillMaxWidth().verticalScroll(rememberScrollState()),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        if (left > 0) {
            KeepScreenOn()
            QrImage(pairing.qr.toPayload(), Modifier.size(280.dp))
            Text(stringResource(R.string.pair_device_scan_hint), style = MaterialTheme.typography.bodyMedium)
            Text(pairing.code, style = MaterialTheme.typography.headlineLarge)
            Text(stringResource(R.string.pair_device_server, pairing.qr.server.value), style = MaterialTheme.typography.bodySmall)
            Text(stringResource(R.string.pair_device_expires_in, formatCountdown(left)))
        } else {
            Text(stringResource(R.string.pair_device_expired), style = MaterialTheme.typography.titleMedium)
            Button(onClick = onRefresh, enabled = !creating) { Text(stringResource(R.string.pair_device_new_code)) }
        }
        TextButton(onClick = onDone) { Text(stringResource(R.string.pair_device_done)) }
    }
}

/** Always dark on white, whatever the theme, so scanners can read it. */
@Composable
private fun QrImage(payload: String, modifier: Modifier = Modifier) {
    val bitmap = remember(payload) {
        val m = QrMatrix.encode(payload)
        val pixels = IntArray(m.size * m.size) { if (m[it % m.size, it / m.size]) 0xFF000000.toInt() else 0xFFFFFFFF.toInt() }
        Bitmap.createBitmap(pixels, m.size, m.size, Bitmap.Config.ARGB_8888).asImageBitmap()
    }
    Image(
        bitmap = bitmap,
        contentDescription = stringResource(R.string.pair_device_qr_description),
        filterQuality = FilterQuality.None,
        modifier = modifier,
    )
}

@Composable
private fun KeepScreenOn() {
    val view = LocalView.current
    DisposableEffect(view) {
        view.keepScreenOn = true
        onDispose { view.keepScreenOn = false }
    }
}
