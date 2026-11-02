package dev.voidrot.chorecast.ui.home

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.KidOverview

/** Parent home: each kid's progress today, balance, streak and waiting reviews, and the way into the queue. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(
    viewModel: HomeViewModel,
    overviewViewModel: OverviewViewModel,
    serverUrl: String,
    recoveryCodesLeft: Long?,
    onSignedOut: () -> Unit,
    onChangeServer: () -> Unit,
    onDevices: () -> Unit,
    onReview: () -> Unit,
    onKid: (Long) -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val overview by overviewViewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.signedOut) { if (state.signedOut) onSignedOut() }
    LaunchedEffect(overview.signedOut) { if (overview.signedOut) onSignedOut() }
    LifecycleResumeEffect(overviewViewModel) {
        overviewViewModel.onResume()
        onPauseOrDispose {}
    }

    PullToRefreshBox(isRefreshing = overview.refreshing, onRefresh = overviewViewModel::refresh, modifier = modifier.fillMaxSize()) {
        LazyColumn(
            Modifier.fillMaxSize().padding(horizontal = 24.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(vertical = 24.dp),
        ) {
            item {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    state.displayName?.let {
                        Text(stringResource(R.string.home_signed_in, it), style = MaterialTheme.typography.headlineSmall)
                    }
                    state.household?.let {
                        Text(it.name, style = MaterialTheme.typography.titleMedium)
                        Text(stringResource(R.string.home_household, it.timezone, it.dayEnd), style = MaterialTheme.typography.bodyMedium)
                    }
                    Text(stringResource(R.string.home_server, serverUrl), style = MaterialTheme.typography.bodySmall)
                    if (recoveryCodesLeft != null && recoveryCodesLeft <= 3) {
                        Text(
                            pluralStringResource(R.plurals.home_recovery_codes_left, recoveryCodesLeft.toInt(), recoveryCodesLeft.toInt()),
                            color = MaterialTheme.colorScheme.error,
                        )
                    }
                }
            }
            overviewItems(overview, onReview, onKid, onRetry = overviewViewModel::refresh)
            item {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(onClick = onDevices) { Text(stringResource(R.string.devices_title)) }
                    OutlinedButton(onClick = viewModel::signOut) { Text(stringResource(R.string.action_sign_out)) }
                }
            }
            item {
                OutlinedButton(onClick = onChangeServer) { Text(stringResource(R.string.action_change_server)) }
            }
        }
    }
}

/** The review button, a load banner and each kid's card: the part of the parent home that tablets reuse. */
internal fun LazyListScope.overviewItems(
    overview: OverviewState,
    onReview: () -> Unit,
    onKid: (Long) -> Unit,
    onRetry: () -> Unit,
) {
    item {
        val data = overview.overview
        Button(onClick = onReview, modifier = Modifier.fillMaxWidth()) {
            Text(
                when {
                    data == null || data.pendingTotal == 0 -> stringResource(R.string.home_review)
                    data.pendingMore -> stringResource(R.string.home_review_pending_more, data.pendingTotal)
                    else -> stringResource(R.string.home_review_pending, data.pendingTotal)
                },
            )
        }
    }
    overview.error?.let { code ->
        item {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    if (overview.overview == null) stringResource(R.string.home_load_failed, code) else stringResource(R.string.home_refresh_failed),
                    color = MaterialTheme.colorScheme.error,
                )
                OutlinedButton(onClick = onRetry) { Text(stringResource(R.string.action_retry)) }
            }
        }
    }
    val kids = overview.overview?.kids.orEmpty()
    if (overview.overview != null && kids.isEmpty()) {
        item { Text(stringResource(R.string.home_no_kids)) }
    }
    items(kids, key = { it.kid.id }) { KidCard(it, onClick = { onKid(it.kid.id) }) }
}

@Composable
private fun KidCard(row: KidOverview, onClick: () -> Unit) {
    ElevatedCard(Modifier.fillMaxWidth().clickable(onClick = onClick)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Text(row.kid.displayName, style = MaterialTheme.typography.titleMedium)
            if (row.total == 0) {
                Text(stringResource(R.string.home_kid_nothing_today), style = MaterialTheme.typography.bodyMedium)
            } else {
                Text(stringResource(R.string.home_kid_progress, row.done, row.total), style = MaterialTheme.typography.bodyMedium)
                LinearProgressIndicator(progress = { row.done.toFloat() / row.total }, modifier = Modifier.fillMaxWidth())
            }
            Row(horizontalArrangement = Arrangement.spacedBy(16.dp), verticalAlignment = Alignment.CenterVertically) {
                row.balance?.let {
                    Text(pluralStringResource(R.plurals.kid_points, it.balance.toInt(), it.balance.toInt()), style = MaterialTheme.typography.bodyMedium)
                }
                row.streak?.let {
                    Text(pluralStringResource(R.plurals.kid_streak_days, it.current.toInt(), it.current.toInt()), style = MaterialTheme.typography.bodyMedium)
                }
            }
            if (row.pendingReviews > 0) {
                Text(
                    pluralStringResource(R.plurals.home_kid_pending, row.pendingReviews, row.pendingReviews),
                    color = MaterialTheme.colorScheme.primary,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
        }
    }
}
