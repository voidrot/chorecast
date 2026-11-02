package dev.voidrot.chorecast.ui.device

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.EvidencePhoto
import dev.voidrot.chorecast.core.ChoreStatusView
import dev.voidrot.chorecast.core.EvidenceView
import dev.voidrot.chorecast.core.PhotoView
import dev.voidrot.chorecast.core.statusView

/**
 * The kid's past chores, newest day first, with a button for the page before the oldest loaded.
 * Tapping a chore opens what was handed in for it. There is no gallery: photos are rows, opened
 * one at a time.
 */
@Composable
fun HistoryScreen(
    state: HistoryState,
    onOpen: (ChoreInstance) -> Unit,
    onCloseChore: () -> Unit,
    onLoadOlder: () -> Unit,
    onRetry: () -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val selected = state.selected
    if (selected != null) {
        HistoryChoreScreen(selected, state.evidence, onCloseChore, modifier)
        return
    }
    val history = state.history
    Column(modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
            Text(stringResource(R.string.kid_history_title), style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
            TextButton(onClick = onBack) { Text(stringResource(R.string.action_back)) }
        }
        when {
            history == null -> Column(
                Modifier.weight(1f).fillMaxWidth(),
                verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                if (state.error != null) {
                    Text(stringResource(R.string.kid_history_failed), color = MaterialTheme.colorScheme.error)
                    Text(errorText(state.error))
                    Button(onClick = onRetry) { Text(stringResource(R.string.action_retry)) }
                } else {
                    CircularProgressIndicator()
                }
            }
            else -> LazyColumn(Modifier.weight(1f).fillMaxWidth()) {
                if (history.days.isEmpty()) item(key = "empty") { Text(stringResource(R.string.kid_history_empty)) }
                for (day in history.days) {
                    item(key = "day-${day.date}") {
                        Text(
                            formatDate(day.date),
                            style = MaterialTheme.typography.titleSmall,
                            color = MaterialTheme.colorScheme.primary,
                            modifier = Modifier.padding(top = 12.dp),
                        )
                    }
                    items(day.chores, key = { "h-${it.id}" }) { HistoryRow(it, onOpen) }
                }
                item(key = "older") {
                    Column(Modifier.fillMaxWidth().padding(top = 16.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                        if (state.error != null) {
                            Text(stringResource(R.string.kid_history_failed), color = MaterialTheme.colorScheme.error)
                        }
                        when {
                            state.loading -> CircularProgressIndicator()
                            history.canLoadOlder -> OutlinedButton(onClick = onLoadOlder) { Text(stringResource(R.string.kid_history_older)) }
                            else -> Text(stringResource(R.string.kid_history_end))
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun HistoryRow(instance: ChoreInstance, onOpen: (ChoreInstance) -> Unit) {
    val status = instance.statusView()
    Column(Modifier.fillMaxWidth().clickable(role = Role.Button) { onOpen(instance) }) {
        Row(
            Modifier.fillMaxWidth().padding(vertical = 12.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(Modifier.weight(1f)) {
                Text(instance.choreName, style = MaterialTheme.typography.titleMedium)
                StatusLabel(status)
            }
            if (status == ChoreStatusView.APPROVED) instance.points?.let { PointsLabel(it) }
        }
        HorizontalDivider()
    }
}

@Composable
private fun HistoryChoreScreen(instance: ChoreInstance, load: EvidenceLoad, onBack: () -> Unit, modifier: Modifier) {
    Column(modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
            Text(instance.choreName, style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
            TextButton(onClick = onBack) { Text(stringResource(R.string.action_back)) }
        }
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(formatDate(instance.date), style = MaterialTheme.typography.bodyLarge)
                StatusLabel(instance.statusView())
            }
            when (load) {
                EvidenceLoad.Loading -> CircularProgressIndicator()
                is EvidenceLoad.Failed -> Text(stringResource(R.string.kid_history_evidence_failed), color = MaterialTheme.colorScheme.error)
                is EvidenceLoad.Loaded -> {
                    val view = load.view
                    if (view == null || view.isEmpty) Text(stringResource(R.string.kid_history_evidence_none)) else EvidenceSection(view)
                }
            }
        }
    }
}

@Composable
private fun EvidenceSection(view: EvidenceView) {
    val uri = LocalUriHandler.current
    Column(verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Text(stringResource(R.string.kid_history_evidence_title), style = MaterialTheme.typography.titleMedium)
        view.note?.let {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(stringResource(R.string.kid_history_note), style = MaterialTheme.typography.titleSmall)
                Text(it)
            }
        }
        if (view.steps.isNotEmpty()) {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(stringResource(R.string.chore_steps), style = MaterialTheme.typography.titleSmall)
                view.steps.forEach {
                    Text(stringResource(if (it.done) R.string.kid_history_step_done else R.string.kid_history_step_skipped, it.text))
                }
            }
        }
        if (view.photos.isNotEmpty()) {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(stringResource(R.string.kid_history_photos), style = MaterialTheme.typography.titleSmall)
                view.photos.forEachIndexed { i, photo ->
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(photoLabel(i, photo.role))
                            if (photo is PhotoView.Expired) {
                                Text(stringResource(R.string.kid_history_photo_expired), style = MaterialTheme.typography.bodySmall)
                            }
                        }
                        if (photo is PhotoView.Available) {
                            TextButton(onClick = { uri.openUri(photo.url) }) { Text(stringResource(R.string.kid_history_photo_view)) }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun photoLabel(index: Int, role: EvidencePhoto.Role?): String = when (role) {
    EvidencePhoto.Role.BEFORE -> stringResource(R.string.complete_photo_before)
    EvidencePhoto.Role.AFTER -> stringResource(R.string.complete_photo_after)
    null -> stringResource(R.string.complete_photo_n, index + 1)
}
