package dev.voidrot.chorecast.ui.settings

import android.os.Build
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import dev.voidrot.chorecast.BuildConfig
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.Appearance
import dev.voidrot.chorecast.core.ThemeMode

/** App settings stored on this device. Stateless: the caller owns the [Appearance] state. */
@Composable
fun SettingsScreen(
    appearance: Appearance,
    onTheme: (ThemeMode) -> Unit,
    onDynamicColor: (Boolean) -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
            Text(stringResource(R.string.settings_title), style = MaterialTheme.typography.headlineSmall)
            TextButton(onClick = onBack) { Text(stringResource(R.string.action_back)) }
        }
        Text(stringResource(R.string.settings_appearance), style = MaterialTheme.typography.titleMedium)
        Text(stringResource(R.string.settings_theme))
        Column(Modifier.selectableGroup()) {
            for (mode in ThemeMode.entries) {
                Row(
                    Modifier.fillMaxWidth().selectable(
                        selected = appearance.theme == mode,
                        onClick = { onTheme(mode) },
                        role = Role.RadioButton,
                    ).padding(vertical = 12.dp),
                    horizontalArrangement = Arrangement.spacedBy(16.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    RadioButton(selected = appearance.theme == mode, onClick = null)
                    Text(stringResource(themeLabel(mode)))
                }
            }
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            Row(
                Modifier.fillMaxWidth().toggleable(
                    value = appearance.dynamicColor,
                    role = Role.Switch,
                    onValueChange = onDynamicColor,
                ).padding(vertical = 12.dp),
                horizontalArrangement = Arrangement.spacedBy(16.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f)) {
                    Text(stringResource(R.string.settings_dynamic_color))
                    Text(
                        stringResource(R.string.settings_dynamic_color_summary),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Switch(checked = appearance.dynamicColor, onCheckedChange = null)
            }
        }
        Text(stringResource(R.string.settings_about), style = MaterialTheme.typography.titleMedium)
        Text(stringResource(R.string.settings_version, BuildConfig.VERSION_NAME))
    }
}

private fun themeLabel(mode: ThemeMode): Int = when (mode) {
    ThemeMode.SYSTEM -> R.string.settings_theme_system
    ThemeMode.LIGHT -> R.string.settings_theme_light
    ThemeMode.DARK -> R.string.settings_theme_dark
}
