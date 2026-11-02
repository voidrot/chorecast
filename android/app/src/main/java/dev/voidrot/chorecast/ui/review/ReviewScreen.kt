package dev.voidrot.chorecast.ui.review

import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
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
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.api.models.RejectChoreInstanceInputBody
import dev.voidrot.chorecast.api.models.ReviewQueueItem
import dev.voidrot.chorecast.core.REJECT_REASONS
import dev.voidrot.chorecast.core.REJECT_TEXT_MAX
import dev.voidrot.chorecast.core.linkLapsed
import dev.voidrot.chorecast.core.openableUrl
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

private val submittedFormat = DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, FormatStyle.SHORT)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ReviewScreen(
    viewModel: ReviewViewModel,
    onBack: () -> Unit,
    onSignedOut: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    LaunchedEffect(state.signedOut) { if (state.signedOut) onSignedOut() }
    // Signed photo links last about ten minutes, so a queue left open is reloaded on return.
    LifecycleResumeEffect(viewModel) {
        viewModel.refreshIfStale()
        onPauseOrDispose {}
    }
    // Resolved in composition (configuration-aware), shown once.
    val noticeText = state.notice?.let { noticeText(it) }
    LaunchedEffect(noticeText) {
        if (noticeText == null) return@LaunchedEffect
        android.widget.Toast.makeText(context, noticeText, android.widget.Toast.LENGTH_LONG).show()
        viewModel.noticeShown()
    }

    Column(modifier.fillMaxSize().padding(horizontal = 16.dp, vertical = 16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
            Text(stringResource(R.string.review_title), style = MaterialTheme.typography.headlineSmall)
            TextButton(onClick = onBack) { Text(stringResource(R.string.action_back)) }
        }
        state.error?.let { code ->
            Text(
                if (state.items.isEmpty()) stringResource(R.string.review_load_failed, code) else stringResource(R.string.review_refresh_failed),
                color = MaterialTheme.colorScheme.error,
            )
        }
        PullToRefreshBox(isRefreshing = state.refreshing, onRefresh = viewModel::refresh, modifier = Modifier.weight(1f)) {
            when {
                state.loading -> Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
                    CircularProgressIndicator()
                }
                state.items.isEmpty() -> Column(
                    Modifier.fillMaxSize(),
                    verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    if (state.error != null) {
                        Button(onClick = viewModel::refresh) { Text(stringResource(R.string.action_retry)) }
                    } else {
                        Text(stringResource(R.string.review_empty))
                    }
                }
                else -> LazyColumn(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    state.groups.forEach { group ->
                        item(key = group.key) {
                            Text(
                                stringResource(R.string.review_group, group.kidName, group.items.size),
                                style = MaterialTheme.typography.titleMedium,
                                modifier = Modifier.padding(top = 8.dp),
                            )
                        }
                        items(group.items, key = { it.instance.id }) { item ->
                            ReviewCard(
                                item = item,
                                checked = item.instance.id in state.selected,
                                enabled = !state.working,
                                onToggle = { viewModel.toggle(item.instance.id) },
                                onApprove = { viewModel.approve(item.instance.id) },
                                onReject = { viewModel.openReject(item) },
                                onReload = viewModel::refresh,
                            )
                        }
                    }
                    if (state.nextCursor != null) {
                        item(key = "more") {
                            OutlinedButton(onClick = viewModel::loadMore, enabled = !state.loadingMore, modifier = Modifier.fillMaxWidth()) {
                                Text(stringResource(R.string.review_load_more))
                            }
                        }
                    }
                }
            }
        }
        if (state.items.isNotEmpty()) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                TextButton(onClick = viewModel::selectAll, enabled = !state.working) { Text(stringResource(R.string.review_select_all)) }
                Button(onClick = viewModel::approveSelected, enabled = state.selected.isNotEmpty() && !state.working) {
                    Text(pluralStringResource(R.plurals.review_approve_selected, state.selected.size, state.selected.size))
                }
            }
        }
    }

    state.reject?.let { draft ->
        RejectDialog(
            draft = draft,
            enabled = !state.working,
            canSubmit = state.canSubmitReject,
            onReason = viewModel::setReason,
            onText = viewModel::setText,
            onConfirm = viewModel::confirmReject,
            onDismiss = viewModel::closeReject,
        )
    }
}

@Composable
private fun ReviewCard(
    item: ReviewQueueItem,
    checked: Boolean,
    enabled: Boolean,
    onToggle: () -> Unit,
    onApprove: () -> Unit,
    onReject: () -> Unit,
    onReload: () -> Unit,
) {
    val uriHandler = LocalUriHandler.current
    ElevatedCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Checkbox(checked = checked, onCheckedChange = { onToggle() }, enabled = enabled)
                Column(Modifier.weight(1f)) {
                    Text(item.instance.choreName, style = MaterialTheme.typography.titleMedium)
                    item.submission?.let {
                        Text(
                            stringResource(R.string.review_submitted, it.submittedAt.atZoneSameInstant(ZoneId.systemDefault()).format(submittedFormat)),
                            style = MaterialTheme.typography.bodySmall,
                        )
                    }
                }
            }
            item.instance.rejectionCount?.takeIf { it > 0 }?.toInt()?.let { n ->
                Text(pluralStringResource(R.plurals.review_rejected_before, n, n), style = MaterialTheme.typography.bodySmall)
            }
            item.steps.forEach { step ->
                Text(
                    stringResource(if (step.done) R.string.review_step_done else R.string.review_step_not_done, step.text),
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
            item.submission?.note?.takeIf { it.isNotBlank() }?.let {
                Text(stringResource(R.string.review_note, it), style = MaterialTheme.typography.bodyMedium)
            }
            // Photos open in the browser: signed links, no image library in the app yet.
            item.submission?.photos.orEmpty().forEachIndexed { index, photo ->
                val link = photo.openableUrl()
                if (link == null) {
                    Text(stringResource(R.string.review_photo_expired, index + 1), style = MaterialTheme.typography.bodySmall)
                } else {
                    TextButton(onClick = {
                        // A lapsed signed link is dead: reload the queue for fresh ones instead.
                        if (photo.linkLapsed(Instant.now())) onReload() else uriHandler.openUri(link)
                    }) {
                        Text(stringResource(R.string.review_photo_open, index + 1))
                    }
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = onApprove, enabled = enabled) { Text(stringResource(R.string.review_approve)) }
                OutlinedButton(onClick = onReject, enabled = enabled) { Text(stringResource(R.string.review_reject)) }
            }
        }
    }
}

@Composable
private fun RejectDialog(
    draft: RejectDraft,
    enabled: Boolean,
    canSubmit: Boolean,
    onReason: (RejectChoreInstanceInputBody.Reason) -> Unit,
    onText: (String) -> Unit,
    onConfirm: () -> Unit,
    onDismiss: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.review_reject_title, draft.item.instance.choreName)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Column(Modifier.selectableGroup()) {
                    REJECT_REASONS.forEach { reason ->
                        Row(
                            Modifier.fillMaxWidth().selectable(
                                selected = draft.reason == reason,
                                enabled = enabled,
                                role = Role.RadioButton,
                                onClick = { onReason(reason) },
                            ),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            RadioButton(selected = draft.reason == reason, onClick = null, enabled = enabled)
                            Text(reasonLabel(reason), modifier = Modifier.padding(start = 8.dp))
                        }
                    }
                }
                OutlinedTextField(
                    value = draft.text,
                    onValueChange = onText,
                    label = { Text(stringResource(R.string.review_reject_text)) },
                    supportingText = {
                        Text(stringResource(R.string.review_reject_counter, draft.text.codePointCount(0, draft.text.length), REJECT_TEXT_MAX))
                    },
                    enabled = enabled,
                    modifier = Modifier.fillMaxWidth(),
                )
                Text(stringResource(R.string.review_reject_hint), style = MaterialTheme.typography.bodySmall)
            }
        },
        confirmButton = {
            Button(onClick = onConfirm, enabled = enabled && canSubmit) { Text(stringResource(R.string.review_reject)) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.action_cancel)) } },
    )
}

@Composable
private fun reasonLabel(reason: RejectChoreInstanceInputBody.Reason): String = stringResource(
    when (reason) {
        RejectChoreInstanceInputBody.Reason.NOT_DONE -> R.string.review_reason_not_done
        RejectChoreInstanceInputBody.Reason.NOT_FINISHED -> R.string.review_reason_not_finished
        RejectChoreInstanceInputBody.Reason.NEEDS_REDO -> R.string.review_reason_needs_redo
        RejectChoreInstanceInputBody.Reason.EVIDENCE_UNCLEAR -> R.string.review_reason_evidence_unclear
    },
)

@Composable
private fun noticeText(n: ReviewNotice): String = when (n) {
    is ReviewNotice.Approved -> pluralStringResource(R.plurals.review_notice_approved, n.count, n.count)
    is ReviewNotice.AlreadyDecided -> pluralStringResource(R.plurals.review_notice_already_decided, n.count, n.count)
    ReviewNotice.Rejected -> stringResource(R.string.review_notice_rejected)
    is ReviewNotice.SomeFailed -> pluralStringResource(R.plurals.review_notice_some_failed, n.count, n.count)
    is ReviewNotice.Offline -> stringResource(R.string.device_offline, n.reason)
    is ReviewNotice.Failed -> stringResource(R.string.review_notice_failed, n.code)
}
