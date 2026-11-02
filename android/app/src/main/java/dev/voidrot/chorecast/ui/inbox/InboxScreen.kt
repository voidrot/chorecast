package dev.voidrot.chorecast.ui.inbox

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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.InboxEntry
import dev.voidrot.chorecast.core.InboxMessage
import dev.voidrot.chorecast.core.RejectionReason
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

private val timeFormat = DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, FormatStyle.SHORT)

/**
 * The inbox for a parent or a kid. Tapping an item marks it read and calls [onOpen] with it; the
 * caller decides what that opens (the review queue for a parent, the chore for a kid).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun InboxScreen(
    viewModel: InboxViewModel,
    onBack: () -> Unit,
    onOpen: (InboxEntry) -> Unit,
    onSignedOut: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    BackHandler(onBack = onBack)
    LaunchedEffect(state.signedOut) { if (state.signedOut) onSignedOut() }

    Column(modifier.fillMaxSize().padding(horizontal = 16.dp, vertical = 16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
            Text(stringResource(R.string.inbox_title), style = MaterialTheme.typography.headlineSmall)
            TextButton(onClick = onBack) { Text(stringResource(R.string.action_back)) }
        }
        val feed = state.feed
        state.error?.let { code ->
            Text(
                if (feed == null) stringResource(R.string.inbox_load_failed, code) else stringResource(R.string.inbox_refresh_failed),
                color = MaterialTheme.colorScheme.error,
            )
            OutlinedButton(onClick = viewModel::refresh) { Text(stringResource(R.string.action_retry)) }
        }
        if (feed != null && feed.unread > 0) {
            TextButton(onClick = viewModel::markAllRead) { Text(stringResource(R.string.inbox_mark_all_read)) }
        }
        PullToRefreshBox(isRefreshing = state.refreshing, onRefresh = viewModel::refresh, modifier = Modifier.weight(1f)) {
            when {
                state.loading -> Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
                    CircularProgressIndicator()
                }
                feed != null && feed.entries.isEmpty() -> Text(stringResource(R.string.inbox_empty))
                feed != null -> LazyColumn(Modifier.fillMaxSize()) {
                    items(feed.entries, key = { it.id }) { entry ->
                        InboxRow(entry, onClick = {
                            viewModel.markRead(entry)
                            onOpen(entry)
                        })
                    }
                    if (feed.nextCursor != null) {
                        item(key = "more") {
                            Column(Modifier.fillMaxWidth().padding(vertical = 12.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                                if (state.loadingMore) {
                                    CircularProgressIndicator()
                                } else {
                                    Button(onClick = viewModel::loadMore) { Text(stringResource(R.string.inbox_load_more)) }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun InboxRow(entry: InboxEntry, onClick: () -> Unit) {
    Column(Modifier.fillMaxWidth().clickable(role = Role.Button, onClick = onClick)) {
        Column(Modifier.fillMaxWidth().padding(vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(
                messageText(entry.message),
                style = MaterialTheme.typography.titleMedium,
                fontWeight = if (entry.read) FontWeight.Normal else FontWeight.Bold,
            )
            detailText(entry.message)?.let { Text(it, style = MaterialTheme.typography.bodyMedium) }
            Text(
                timeFormat.format(entry.createdAt.atZoneSameInstant(ZoneId.systemDefault())),
                style = MaterialTheme.typography.bodySmall,
            )
        }
        HorizontalDivider()
    }
}

@Composable
private fun messageText(m: InboxMessage): String = when (m) {
    is InboxMessage.Submitted -> stringResource(R.string.inbox_submitted, m.choreName)
    is InboxMessage.Approved ->
        if (m.auto) stringResource(R.string.inbox_approved_auto, m.choreName) else stringResource(R.string.inbox_approved, m.choreName)
    is InboxMessage.Rejected -> stringResource(R.string.inbox_rejected, m.choreName)
    is InboxMessage.Missed -> stringResource(R.string.inbox_missed, m.choreName)
    is InboxMessage.Excused -> stringResource(R.string.inbox_excused, m.choreName)
    is InboxMessage.Streak -> pluralStringResource(R.plurals.inbox_streak, m.days.toInt(), m.days.toInt())
    else -> stringResource(R.string.inbox_other)
}

/** The second line: a rejection's reason and feedback, or a streak's bonus. */
@Composable
private fun detailText(m: InboxMessage): String? = when (m) {
    is InboxMessage.Rejected -> listOfNotNull(m.reason?.let { reasonText(it) }, m.text).joinToString(" ").ifBlank { null }
    is InboxMessage.Streak -> if (m.points > 0) pluralStringResource(R.plurals.kid_points, m.points.toInt(), m.points.toInt()) else null
    else -> null
}

@Composable
private fun reasonText(r: RejectionReason): String = stringResource(
    when (r) {
        RejectionReason.NOT_DONE -> R.string.chore_reject_not_done
        RejectionReason.NOT_FINISHED -> R.string.chore_reject_not_finished
        RejectionReason.NEEDS_REDO -> R.string.chore_reject_needs_redo
        RejectionReason.EVIDENCE_UNCLEAR -> R.string.chore_reject_evidence_unclear
    },
)
