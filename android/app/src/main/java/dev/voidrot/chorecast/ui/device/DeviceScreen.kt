package dev.voidrot.chorecast.ui.device

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R
import kotlinx.coroutines.delay
import java.time.Instant

@Composable
fun DeviceScreen(viewModel: DeviceViewModel, onUnpaired: () -> Unit, modifier: Modifier = Modifier) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.unpaired) { if (state.unpaired) onUnpaired() }

    Box(
        modifier.fillMaxSize().pointerInput(Unit) {
            // Any touch counts as activity for the kid session's auto-lock.
            awaitPointerEventScope {
                while (true) {
                    awaitPointerEvent(PointerEventPass.Initial)
                    viewModel.onInteraction()
                }
            }
        },
    ) {
        val active = state.active
        val picker = state.picker
        when {
            state.loading -> Centered { CircularProgressIndicator() }
            state.offline != null -> Centered {
                Text(stringResource(R.string.device_offline, state.offline.orEmpty()))
                Button(onClick = viewModel::load) { Text(stringResource(R.string.action_retry)) }
            }
            active is ActiveSession.Kid -> Centered {
                Initials(active.kid.displayName, active.kid.color, 96)
                Text(stringResource(R.string.device_hi, active.kid.displayName), style = MaterialTheme.typography.headlineMedium)
                Text(stringResource(R.string.device_chores_soon))
                OutlinedButton(onClick = viewModel::lock) { Text(stringResource(R.string.device_done)) }
            }
            active is ActiveSession.Parent -> Centered {
                Text(stringResource(R.string.device_parent_mode, active.parent.displayName), style = MaterialTheme.typography.headlineSmall)
                Countdown(active.expiresAt)
                Text(stringResource(R.string.device_parent_soon))
                OutlinedButton(onClick = viewModel::lock) { Text(stringResource(R.string.device_lock)) }
            }
            picker != null && !picker.device.shared -> Centered {
                Text(stringResource(R.string.device_hi, state.kidName ?: ""), style = MaterialTheme.typography.headlineMedium)
                Text(stringResource(R.string.device_chores_soon))
                if (picker.parents.isNotEmpty()) {
                    TextButton(onClick = { viewModel.chooseParent(true) }) { Text(stringResource(R.string.device_parent)) }
                }
            }
            picker != null -> Column(Modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                Text(stringResource(R.string.device_who), style = MaterialTheme.typography.headlineSmall)
                LazyVerticalGrid(
                    columns = GridCells.Adaptive(140.dp),
                    horizontalArrangement = Arrangement.spacedBy(16.dp),
                    verticalArrangement = Arrangement.spacedBy(16.dp),
                    modifier = Modifier.weight(1f),
                ) {
                    items(picker.kids, key = { it.id }) { kid ->
                        Column(
                            Modifier.clip(MaterialTheme.shapes.large).clickable(enabled = !state.busy) { viewModel.pickKid(kid) }.padding(12.dp),
                            horizontalAlignment = Alignment.CenterHorizontally,
                            verticalArrangement = Arrangement.spacedBy(8.dp),
                        ) {
                            Initials(kid.displayName, kid.color, 88)
                            Text(kid.displayName, style = MaterialTheme.typography.titleMedium)
                        }
                    }
                }
                if (picker.kids.isEmpty()) Text(stringResource(R.string.device_no_kids))
                if (picker.parents.isNotEmpty()) {
                    OutlinedButton(onClick = { viewModel.chooseParent(true) }, modifier = Modifier.fillMaxWidth()) {
                        Text(stringResource(R.string.device_parent))
                    }
                }
            }
        }

        if (state.choosingParent && picker != null) {
            AlertDialog(
                onDismissRequest = { viewModel.chooseParent(false) },
                title = { Text(stringResource(R.string.device_which_parent)) },
                text = {
                    Column {
                        picker.parents.forEach { p ->
                            TextButton(onClick = { viewModel.pickParent(p) }, modifier = Modifier.fillMaxWidth()) { Text(p.displayName) }
                        }
                    }
                },
                confirmButton = {},
                dismissButton = { TextButton(onClick = { viewModel.chooseParent(false) }) { Text(stringResource(R.string.action_cancel)) } },
            )
        }
        state.pinTarget?.let { target ->
            PinDialog(
                title = when (target) {
                    is PinTarget.Kid -> stringResource(R.string.device_pin_kid, target.kid.displayName)
                    is PinTarget.Parent -> stringResource(R.string.device_pin_parent, target.parent.displayName)
                },
                error = state.pinError?.let { pinErrorText(it) },
                busy = state.busy,
                onSubmit = viewModel::submitPin,
                onCancel = viewModel::cancelPin,
            )
        }
    }
}

@Composable
private fun Centered(content: @Composable () -> Unit) {
    Column(
        Modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) { content() }
}

/** A colored circle with the kid's initials (built-in avatar images come with the kid screens). */
@Composable
private fun Initials(name: String, color: String, sizeDp: Int) {
    val bg = runCatching { Color(android.graphics.Color.parseColor(color)) }.getOrDefault(MaterialTheme.colorScheme.primary)
    val text = name.trim().split(Regex("\\s+")).take(2).mapNotNull { it.firstOrNull()?.uppercaseChar() }.joinToString("")
    Box(Modifier.size(sizeDp.dp).clip(CircleShape).background(bg), contentAlignment = Alignment.Center) {
        Text(text, color = Color.White, fontSize = (sizeDp / 2.6).sp, style = MaterialTheme.typography.titleLarge)
    }
}

@Composable
private fun Countdown(until: Instant) {
    var left by remember { mutableLongStateOf(until.epochSecond - Instant.now().epochSecond) }
    LaunchedEffect(until) {
        while (left > 0) {
            delay(1000)
            left = until.epochSecond - Instant.now().epochSecond
        }
    }
    val s = left.coerceAtLeast(0)
    Text(stringResource(R.string.device_parent_left, s / 60, s % 60))
}

@Composable
private fun PinDialog(title: String, error: String?, busy: Boolean, onSubmit: (String) -> Unit, onCancel: () -> Unit) {
    var pin by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onCancel,
        title = { Text(title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = pin,
                    onValueChange = { v -> pin = v.filter(Char::isDigit).take(8) },
                    label = { Text(stringResource(R.string.device_pin)) },
                    singleLine = true,
                    visualTransformation = PasswordVisualTransformation(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword, imeAction = ImeAction.Done),
                    keyboardActions = KeyboardActions(onDone = { if (pin.length >= 4) onSubmit(pin) }),
                )
                error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            }
        },
        confirmButton = {
            Button(onClick = { onSubmit(pin) }, enabled = pin.length >= 4 && !busy) { Text(stringResource(R.string.action_unlock)) }
        },
        dismissButton = { TextButton(onClick = onCancel) { Text(stringResource(R.string.action_cancel)) } },
    )
}

@Composable
private fun pinErrorText(e: PinError): String = when (e) {
    PinError.Wrong -> stringResource(R.string.device_pin_wrong)
    is PinError.Locked -> pluralStringResource(R.plurals.device_pin_locked, e.minutes.toInt(), e.minutes.toInt())
    is PinError.Failed -> stringResource(R.string.login_error_failed, e.reason)
}
