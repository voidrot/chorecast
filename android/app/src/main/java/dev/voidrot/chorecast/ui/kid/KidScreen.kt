package dev.voidrot.chorecast.ui.kid

import androidx.annotation.StringRes
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.core.AdjustmentProblem
import dev.voidrot.chorecast.core.ChoreStatusView
import dev.voidrot.chorecast.core.NOTE_MAX
import dev.voidrot.chorecast.core.ParentAction
import dev.voidrot.chorecast.core.StaleReason
import dev.voidrot.chorecast.core.actionsFor
import dev.voidrot.chorecast.core.reassignTargets
import dev.voidrot.chorecast.core.statusView

/** One kid for the parent: balance and manual adjustment, and the recent chores with their actions. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun KidScreen(
    viewModel: KidViewModel,
    onBack: () -> Unit,
    onSignedOut: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    LaunchedEffect(state.signedOut) { if (state.signedOut) onSignedOut() }
    // Resolved in composition (configuration-aware), shown once.
    val noticeText = state.notice?.let { noticeText(it) }
    LaunchedEffect(noticeText) {
        if (noticeText == null) return@LaunchedEffect
        android.widget.Toast.makeText(context, noticeText, android.widget.Toast.LENGTH_LONG).show()
        viewModel.noticeShown()
    }

    val data = state.data
    val plan = state.plan
    Column(modifier.fillMaxSize().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
            Text(data?.kid?.displayName.orEmpty(), style = MaterialTheme.typography.headlineSmall)
            TextButton(onClick = onBack) { Text(stringResource(R.string.action_back)) }
        }
        state.error?.let { code ->
            Text(
                if (data == null) stringResource(R.string.kid_load_failed, code) else stringResource(R.string.kid_refresh_failed),
                color = MaterialTheme.colorScheme.error,
            )
        }
        PullToRefreshBox(isRefreshing = state.refreshing, onRefresh = viewModel::refresh, modifier = Modifier.weight(1f)) {
            if (data == null || plan == null) {
                Column(
                    Modifier.fillMaxSize(),
                    verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    if (state.loading) {
                        CircularProgressIndicator()
                    } else {
                        Button(onClick = viewModel::refresh) { Text(stringResource(R.string.action_retry)) }
                    }
                }
            } else {
                LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    item {
                        Row(
                            Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.SpaceBetween,
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            data.balance?.let {
                                Text(pluralStringResource(R.plurals.kid_points, it.balance.toInt(), it.balance.toInt()), style = MaterialTheme.typography.titleMedium)
                            }
                            OutlinedButton(onClick = viewModel::openAdjust, enabled = !state.working) {
                                Text(stringResource(R.string.kid_adjust))
                            }
                        }
                    }
                    if (plan.isEmpty) {
                        item { Text(stringResource(R.string.kid_nothing_recent)) }
                    }
                    section(R.string.kid_section_attention, plan.attention, state.working, viewModel)
                    section(R.string.kid_section_today, plan.today, state.working, viewModel)
                    section(R.string.kid_section_earlier, plan.earlier, state.working, viewModel)
                }
            }
        }
    }

    when (val dialog = state.dialog) {
        null -> {}
        is KidDialog.Adjust -> AdjustDialog(
            draft = dialog,
            enabled = !state.working,
            onSubtract = viewModel::setSubtract,
            onAmount = viewModel::setAmount,
            onNote = viewModel::setAdjustNote,
            onConfirm = viewModel::submitAdjust,
            onDismiss = viewModel::closeDialog,
        )
        is KidDialog.Excuse -> NoteDialog(
            title = stringResource(R.string.kid_excuse_title, dialog.instance.choreName),
            confirm = stringResource(R.string.kid_excuse),
            note = dialog.note,
            enabled = !state.working,
            onNote = viewModel::setNote,
            onConfirm = viewModel::confirmExcuse,
            onDismiss = viewModel::closeDialog,
        )
        is KidDialog.Reverse -> NoteDialog(
            title = stringResource(R.string.kid_reverse_title, dialog.instance.choreName),
            confirm = stringResource(R.string.kid_reverse),
            note = dialog.note,
            enabled = !state.working,
            onNote = viewModel::setNote,
            onConfirm = viewModel::confirmReverse,
            onDismiss = viewModel::closeDialog,
        )
        is KidDialog.Reassign -> AlertDialog(
            onDismissRequest = viewModel::closeDialog,
            title = { Text(stringResource(R.string.kid_reassign_title, dialog.instance.choreName)) },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    reassignTargets(data?.kids.orEmpty(), dialog.instance).forEach { kid ->
                        OutlinedButton(onClick = { viewModel.reassignTo(kid) }, enabled = !state.working, modifier = Modifier.fillMaxWidth()) {
                            Text(kid.displayName)
                        }
                    }
                }
            },
            confirmButton = {},
            dismissButton = { TextButton(onClick = viewModel::closeDialog) { Text(stringResource(R.string.action_cancel)) } },
        )
    }
}

private fun androidx.compose.foundation.lazy.LazyListScope.section(
    @StringRes titleRes: Int,
    chores: List<ChoreInstance>,
    working: Boolean,
    viewModel: KidViewModel,
) {
    if (chores.isEmpty()) return
    item(key = "h$titleRes") {
        Text(stringResource(titleRes), style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(top = 8.dp))
    }
    items(chores, key = { "$titleRes:${it.id}" }) { ChoreRow(it, working, viewModel) }
}

@Composable
private fun ChoreRow(instance: ChoreInstance, working: Boolean, viewModel: KidViewModel) {
    ElevatedCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text(instance.choreName, style = MaterialTheme.typography.titleMedium)
            Text(
                stringResource(R.string.kid_chore_status, instance.date.toString(), statusLabel(instance.statusView())),
                style = MaterialTheme.typography.bodyMedium,
            )
            instance.points?.let {
                Text(pluralStringResource(R.plurals.kid_points, it.toInt(), it.toInt()), style = MaterialTheme.typography.bodySmall)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                actionsFor(instance).forEach { action ->
                    OutlinedButton(
                        onClick = {
                            when (action) {
                                ParentAction.REVERSE -> viewModel.openReverse(instance)
                                ParentAction.EXCUSE -> viewModel.openExcuse(instance)
                                ParentAction.REASSIGN -> viewModel.openReassign(instance)
                            }
                        },
                        enabled = !working,
                    ) {
                        Text(
                            stringResource(
                                when (action) {
                                    ParentAction.REVERSE -> R.string.kid_reverse
                                    ParentAction.EXCUSE -> R.string.kid_excuse
                                    ParentAction.REASSIGN -> R.string.kid_reassign
                                },
                            ),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun statusLabel(status: ChoreStatusView): String = stringResource(
    when (status) {
        ChoreStatusView.OPEN -> R.string.kid_status_open
        ChoreStatusView.REDO -> R.string.kid_status_redo
        ChoreStatusView.SUBMITTED -> R.string.kid_status_submitted
        ChoreStatusView.APPROVED -> R.string.kid_status_approved
        ChoreStatusView.MISSED -> R.string.kid_status_missed
        ChoreStatusView.EXCUSED -> R.string.kid_status_excused
    },
)

@Composable
private fun AdjustDialog(
    draft: KidDialog.Adjust,
    enabled: Boolean,
    onSubtract: (Boolean) -> Unit,
    onAmount: (String) -> Unit,
    onNote: (String) -> Unit,
    onConfirm: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.kid_adjust)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Column(Modifier.selectableGroup()) {
                    listOf(false to R.string.kid_adjust_add, true to R.string.kid_adjust_subtract).forEach { (subtract, label) ->
                        Row(
                            Modifier.fillMaxWidth().selectable(
                                selected = draft.subtract == subtract,
                                enabled = enabled,
                                role = Role.RadioButton,
                                onClick = { onSubtract(subtract) },
                            ),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            RadioButton(selected = draft.subtract == subtract, onClick = null, enabled = enabled)
                            Text(stringResource(label), modifier = Modifier.padding(start = 8.dp))
                        }
                    }
                }
                OutlinedTextField(
                    value = draft.amount,
                    onValueChange = onAmount,
                    label = { Text(stringResource(R.string.kid_adjust_amount)) },
                    isError = draft.problem == AdjustmentProblem.AMOUNT_INVALID,
                    supportingText = {
                        if (draft.problem == AdjustmentProblem.AMOUNT_INVALID) Text(stringResource(R.string.kid_adjust_amount_invalid))
                    },
                    singleLine = true,
                    enabled = enabled,
                    modifier = Modifier.fillMaxWidth(),
                )
                OutlinedTextField(
                    value = draft.note,
                    onValueChange = onNote,
                    label = { Text(stringResource(R.string.kid_adjust_note)) },
                    isError = draft.problem == AdjustmentProblem.NOTE_REQUIRED,
                    supportingText = {
                        Text(
                            if (draft.problem == AdjustmentProblem.NOTE_REQUIRED) {
                                stringResource(R.string.kid_adjust_note_required)
                            } else {
                                stringResource(R.string.kid_note_counter, draft.note.codePointCount(0, draft.note.length), NOTE_MAX)
                            },
                        )
                    },
                    enabled = enabled,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
        confirmButton = { Button(onClick = onConfirm, enabled = enabled) { Text(stringResource(R.string.kid_adjust_save)) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

@Composable
private fun NoteDialog(
    title: String,
    confirm: String,
    note: String,
    enabled: Boolean,
    onNote: (String) -> Unit,
    onConfirm: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            OutlinedTextField(
                value = note,
                onValueChange = onNote,
                label = { Text(stringResource(R.string.kid_optional_note)) },
                supportingText = { Text(stringResource(R.string.kid_note_counter, note.codePointCount(0, note.length), NOTE_MAX)) },
                enabled = enabled,
                modifier = Modifier.fillMaxWidth(),
            )
        },
        confirmButton = { Button(onClick = onConfirm, enabled = enabled) { Text(confirm) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

@Composable
private fun noticeText(n: KidNotice): String = when (n) {
    KidNotice.Adjusted -> stringResource(R.string.kid_notice_adjusted)
    KidNotice.Excused -> stringResource(R.string.kid_notice_excused)
    KidNotice.Reversed -> stringResource(R.string.kid_notice_reversed)
    KidNotice.Reassigned -> stringResource(R.string.kid_notice_reassigned)
    is KidNotice.Stale -> stringResource(
        when (n.reason) {
            StaleReason.CLOSED -> R.string.kid_stale_closed
            StaleReason.TAKEN -> R.string.kid_stale_taken
            StaleReason.SHARED -> R.string.kid_stale_shared
            StaleReason.NOT_FOUND, StaleReason.CHANGED -> R.string.kid_stale_changed
        },
    )
    is KidNotice.Offline -> stringResource(R.string.device_offline, n.reason)
    is KidNotice.Failed -> stringResource(R.string.kid_notice_failed, n.code)
}
