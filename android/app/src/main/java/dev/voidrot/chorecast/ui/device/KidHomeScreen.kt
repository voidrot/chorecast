package dev.voidrot.chorecast.ui.device

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.ChoreStatusView
import dev.voidrot.chorecast.core.KidToday
import dev.voidrot.chorecast.core.TodayChore
import java.time.LocalDate
import java.time.LocalTime
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

private val timeFormat = DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT)
private val dateFormat = DateTimeFormatter.ofLocalizedDate(FormatStyle.FULL)

internal fun formatTime(time: LocalTime): String = timeFormat.format(time)
internal fun formatDate(date: LocalDate): String = dateFormat.format(date)

/**
 * A kid's home: points and streak, today's chores by due time, the next planned days. Tapping a
 * chore opens its details. [onLock] ends a kid session on a shared tablet; [onParent] is the
 * kid-bound device's way into parent mode.
 */
@Composable
fun KidHomeScreen(
    viewModel: TodayViewModel,
    name: String,
    onLock: (() -> Unit)?,
    onParent: (() -> Unit)?,
    onUnauthorized: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    BackHandler(enabled = state.selected != null || state.history.open) {
        when {
            state.history.selected != null -> viewModel.closeHistoryChore()
            state.history.open -> viewModel.closeHistory()
            state.cameraOpen -> viewModel.closeCamera()
            state.completing -> if (!state.submitting) viewModel.cancelComplete()
            else -> viewModel.close()
        }
    }
    // A phone left open overnight shows yesterday's chores until it is looked at again.
    LifecycleResumeEffect(viewModel) {
        viewModel.refreshIfStale()
        onPauseOrDispose {}
    }
    // The token was refused (session ended, or the device was unpaired): let the device decide what's next.
    val historyErrors = listOfNotNull(state.history.error, (state.history.evidence as? EvidenceLoad.Failed)?.error)
    LaunchedEffect(state.error, historyErrors) {
        if (state.error == TodayError.SignedOut || TodayError.SignedOut in historyErrors) onUnauthorized()
    }

    val selected = state.selectedChore
    val needs = state.selectedNeeds
    val draft = state.selectedDraft
    if (state.history.open) {
        HistoryScreen(
            state = state.history,
            onOpen = viewModel::openHistoryChore,
            onCloseChore = viewModel::closeHistoryChore,
            onLoadOlder = viewModel::loadHistory,
            onRetry = viewModel::retryHistory,
            onBack = viewModel::closeHistory,
            modifier = modifier,
        )
    } else if (selected != null && state.completing && state.cameraOpen) {
        CameraCapture(
            newFile = viewModel::newCaptureFile,
            onCaptured = viewModel::onCaptured,
            onFailed = viewModel::onCaptureFailed,
            onCancel = viewModel::closeCamera,
            modifier = modifier,
        )
    } else if (selected != null && state.completing && needs != null && draft != null) {
        CompleteForm(
            choreName = selected.instance.choreName,
            needs = needs,
            draft = draft,
            submitting = state.submitting,
            error = state.completeError,
            onToggleStep = viewModel::toggleStep,
            onNote = viewModel::setNote,
            onTakePhoto = viewModel::openCamera,
            onRemovePhoto = viewModel::removePhoto,
            onSubmit = viewModel::submit,
            onCancel = viewModel::cancelComplete,
            modifier = modifier,
        )
    } else if (selected != null) {
        ChoreDetailScreen(
            chore = selected,
            completeError = state.completeError,
            onComplete = viewModel::beginComplete,
            onBack = viewModel::close,
            modifier = modifier,
        )
    } else {
        TodayContent(state, name, viewModel::refresh, viewModel::open, viewModel::openHistory, onLock, onParent, modifier)
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun TodayContent(
    state: TodayState,
    name: String,
    onRefresh: () -> Unit,
    onOpen: (Long) -> Unit,
    onHistory: () -> Unit,
    onLock: (() -> Unit)?,
    onParent: (() -> Unit)?,
    modifier: Modifier,
) {
    Column(modifier.fillMaxSize().padding(horizontal = 24.dp, vertical = 16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
            Text(stringResource(R.string.device_hi, name), style = MaterialTheme.typography.headlineMedium)
            TextButton(onClick = onHistory) { Text(stringResource(R.string.kid_history_open)) }
            onLock?.let { OutlinedButton(onClick = it) { Text(stringResource(R.string.device_done)) } }
            onParent?.let { TextButton(onClick = it) { Text(stringResource(R.string.device_parent)) } }
        }
        state.today?.let { ScoreRow(it) }

        PullToRefreshBox(isRefreshing = state.refreshing, onRefresh = onRefresh, modifier = Modifier.weight(1f)) {
            val today = state.today
            when {
                state.loading && today == null -> Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
                    CircularProgressIndicator()
                }
                today == null -> Column(
                    Modifier.fillMaxSize(),
                    verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    state.error?.let { Text(errorText(it), color = MaterialTheme.colorScheme.error) }
                    Button(onClick = onRefresh) { Text(stringResource(R.string.action_retry)) }
                }
                else -> ChoreList(today, staleError = state.error, onOpen = onOpen)
            }
        }
    }
}

@Composable
private fun ScoreRow(today: KidToday) {
    val balance = today.balance
    val streak = today.streak
    if (balance == null && streak == null) return
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(32.dp)) {
        balance?.let {
            Column {
                Text(stringResource(R.string.kid_balance_label), style = MaterialTheme.typography.labelMedium)
                Text(it.balance.toString(), style = MaterialTheme.typography.headlineSmall)
            }
        }
        streak?.let {
            Column {
                Text(stringResource(R.string.kid_streak_label), style = MaterialTheme.typography.labelMedium)
                Text(
                    pluralStringResource(R.plurals.kid_streak_days, it.current.toInt(), it.current.toInt()),
                    style = MaterialTheme.typography.headlineSmall,
                )
            }
        }
    }
}

@Composable
private fun ChoreList(today: KidToday, staleError: TodayError?, onOpen: (Long) -> Unit) {
    val plan = today.plan
    LazyColumn(Modifier.fillMaxSize()) {
        if (staleError != null) {
            item(key = "stale") { Text(stringResource(R.string.kid_refresh_failed), color = MaterialTheme.colorScheme.error) }
        }
        item(key = "today") {
            Text(
                stringResource(R.string.kid_today_title),
                style = MaterialTheme.typography.titleLarge,
                modifier = Modifier.padding(top = 8.dp, bottom = 4.dp),
            )
        }
        if (plan.today.isEmpty()) item(key = "empty") { Text(stringResource(R.string.kid_empty)) }
        for (section in plan.today) {
            item(key = "slot-${section.time}") {
                Text(
                    section.time?.let(::formatTime) ?: stringResource(R.string.kid_group_day_close),
                    style = MaterialTheme.typography.titleSmall,
                    color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.padding(top = 12.dp),
                )
            }
            items(section.chores, key = { "i-${it.instance.id}" }) { ChoreRow(it, showStatus = true, onOpen) }
        }
        if (plan.planned.isNotEmpty()) {
            item(key = "planned") {
                Column(Modifier.padding(top = 24.dp, bottom = 4.dp)) {
                    Text(stringResource(R.string.kid_planned_title), style = MaterialTheme.typography.titleLarge)
                    Text(stringResource(R.string.kid_planned_note), style = MaterialTheme.typography.bodySmall)
                }
            }
        }
        for (day in plan.planned) {
            item(key = "day-${day.date}") {
                Text(
                    formatDate(day.date),
                    style = MaterialTheme.typography.titleSmall,
                    color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.padding(top = 12.dp),
                )
            }
            items(day.chores, key = { "i-${it.instance.id}" }) { ChoreRow(it, showStatus = false, onOpen) }
        }
    }
}

@Composable
private fun ChoreRow(chore: TodayChore, showStatus: Boolean, onOpen: (Long) -> Unit) {
    Column(Modifier.fillMaxWidth().clickable(role = Role.Button) { onOpen(chore.instance.id) }) {
        Row(
            Modifier.fillMaxWidth().padding(vertical = 12.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(Modifier.weight(1f)) {
                Text(chore.instance.choreName, style = MaterialTheme.typography.titleMedium)
                if (showStatus) StatusLabel(chore.status)
            }
            chore.points?.let { PointsLabel(it) }
        }
        HorizontalDivider()
    }
}

@Composable
internal fun PointsLabel(points: Long) {
    Text(pluralStringResource(R.plurals.kid_points, points.toInt(), points.toInt()), style = MaterialTheme.typography.labelLarge)
}

@Composable
internal fun StatusLabel(status: ChoreStatusView) {
    val colors = MaterialTheme.colorScheme
    val (text, color) = when (status) {
        ChoreStatusView.OPEN -> stringResource(R.string.kid_status_open) to colors.onSurface
        ChoreStatusView.REDO -> stringResource(R.string.kid_status_redo) to colors.error
        ChoreStatusView.SUBMITTED -> stringResource(R.string.kid_status_submitted) to colors.tertiary
        ChoreStatusView.APPROVED -> stringResource(R.string.kid_status_approved) to colors.primary
        ChoreStatusView.MISSED -> stringResource(R.string.kid_status_missed) to colors.error
        ChoreStatusView.EXCUSED -> stringResource(R.string.kid_status_excused) to colors.onSurfaceVariant
        ChoreStatusView.UNKNOWN -> stringResource(R.string.kid_status_unknown) to colors.onSurfaceVariant
    }
    Text(text, color = color, style = MaterialTheme.typography.bodyMedium)
}

@Composable
internal fun errorText(e: TodayError): String = when (e) {
    is TodayError.Offline -> stringResource(R.string.device_offline, e.reason)
    TodayError.SignedOut -> stringResource(R.string.kid_loading_error_signed_out)
    is TodayError.Failed -> stringResource(R.string.kid_loading_error_failed, e.code)
}
