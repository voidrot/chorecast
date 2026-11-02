package dev.voidrot.chorecast.ui.device

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.PinEntry

private val KeyWidth = 88.dp
private val KeyHeight = 64.dp
private val DigitRows = listOf("123", "456", "789")

/** A numeric PIN pad with large keys, for a tablet that is passed around. Kid and parent PINs are both 4 to 8 digits. */
@Composable
internal fun PinPadDialog(title: String, error: String?, busy: Boolean, onSubmit: (String) -> Unit, onCancel: () -> Unit) {
    var entry by remember { mutableStateOf(PinEntry()) }
    // A refused PIN is cleared so the next try starts empty; `busy` falls once per answer, also for a repeated error.
    LaunchedEffect(busy) { if (!busy && error != null) entry = PinEntry() }

    val entered = pluralStringResource(R.plurals.device_pin_entered, entry.digits.length, entry.digits.length)
    AlertDialog(
        onDismissRequest = onCancel,
        title = { Text(title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    (0 until PinEntry.MAX_LENGTH).joinToString(" ") { if (it < entry.digits.length) "●" else "○" },
                    style = MaterialTheme.typography.titleLarge,
                    modifier = Modifier.semantics { contentDescription = entered },
                )
                Text(error.orEmpty(), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
                DigitRows.forEach { row ->
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        row.forEach { digit -> Key(digit.toString(), enabled = !busy) { entry = entry.append(digit) } }
                    }
                }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                    val backspace = stringResource(R.string.device_pin_backspace)
                    Key("⌫", enabled = !busy && entry.digits.isNotEmpty(), description = backspace) { entry = entry.backspace() }
                    Key("0", enabled = !busy) { entry = entry.append('0') }
                    Button(
                        onClick = { onSubmit(entry.digits) },
                        enabled = entry.canSubmit && !busy,
                        modifier = Modifier.width(KeyWidth).height(KeyHeight),
                    ) { Text(stringResource(R.string.action_unlock)) }
                }
            }
        },
        confirmButton = {},
        dismissButton = { TextButton(onClick = onCancel) { Text(stringResource(R.string.action_cancel)) } },
    )
}

@Composable
private fun Key(label: String, enabled: Boolean, description: String? = null, onClick: () -> Unit) {
    FilledTonalButton(
        onClick = onClick,
        enabled = enabled,
        modifier = Modifier.width(KeyWidth).height(KeyHeight).then(
            if (description == null) Modifier else Modifier.semantics { contentDescription = description },
        ),
    ) { Text(label, fontSize = 28.sp) }
}
