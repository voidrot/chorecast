package dev.voidrot.chorecast.ui.device

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.ActiveSession
import dev.voidrot.chorecast.core.ParentPage
import dev.voidrot.chorecast.ui.home.OverviewViewModel
import dev.voidrot.chorecast.ui.home.overviewItems
import dev.voidrot.chorecast.ui.kid.KidScreen
import dev.voidrot.chorecast.ui.kid.KidViewModel
import dev.voidrot.chorecast.ui.review.ReviewScreen
import dev.voidrot.chorecast.ui.review.ReviewViewModel
import androidx.compose.ui.res.stringResource

/** Builds the parent screens' view models for one elevation: they ask the API with the elevated token in the store. */
class ParentModels(
    val overview: (ParentSessionStore) -> OverviewViewModel,
    val review: (ParentSessionStore) -> ReviewViewModel,
    val kid: (ParentSessionStore, Long) -> KidViewModel,
)

/**
 * The parent's overview, review queue and per-kid actions on a shared tablet, under a header with the
 * time left. The elevation is not extended by touches. The view models live in [store], which
 * [DeviceViewModel] clears when the elevation ends, so nothing of it outlives the elevation.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ParentMode(
    session: ActiveSession.Parent,
    page: ParentPage,
    store: ParentSessionStore,
    models: ParentModels,
    onPage: (ParentPage) -> Unit,
    onLock: () -> Unit,
    onRefused: () -> Unit,
    modifier: Modifier = Modifier,
) {
    BackHandler(enabled = page != ParentPage.Overview) { onPage(ParentPage.Overview) }
    Column(modifier.fillMaxSize()) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 12.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column {
                Text(stringResource(R.string.device_parent_mode, session.parent.displayName), style = MaterialTheme.typography.titleLarge)
                Countdown(session.expiresAt)
            }
            OutlinedButton(onClick = onLock) { Text(stringResource(R.string.device_lock)) }
        }
        when (page) {
            ParentPage.Overview -> {
                val vm = viewModel<OverviewViewModel>(viewModelStoreOwner = store, key = "overview") { models.overview(store) }
                val state by vm.state.collectAsStateWithLifecycle()
                LaunchedEffect(state.signedOut) { if (state.signedOut) onRefused() }
                // Back from the queue or a kid: the pending counts may have changed.
                LaunchedEffect(Unit) { vm.onResume() }
                PullToRefreshBox(isRefreshing = state.refreshing, onRefresh = vm::refresh, modifier = Modifier.weight(1f)) {
                    LazyColumn(
                        Modifier.fillMaxSize().padding(horizontal = 24.dp),
                        verticalArrangement = Arrangement.spacedBy(12.dp),
                        contentPadding = androidx.compose.foundation.layout.PaddingValues(vertical = 12.dp),
                    ) {
                        overviewItems(
                            state,
                            onReview = { onPage(ParentPage.Review) },
                            onKid = { onPage(ParentPage.Kid(it)) },
                            onRetry = vm::refresh,
                        )
                    }
                }
            }
            ParentPage.Review -> ReviewScreen(
                viewModel = viewModel<ReviewViewModel>(viewModelStoreOwner = store, key = "review") { models.review(store) },
                onBack = { onPage(ParentPage.Overview) },
                onSignedOut = onRefused,
                modifier = Modifier.weight(1f),
            )
            is ParentPage.Kid -> KidScreen(
                viewModel = viewModel<KidViewModel>(viewModelStoreOwner = store, key = "kid-${page.kidId}") { models.kid(store, page.kidId) },
                onBack = { onPage(ParentPage.Overview) },
                onSignedOut = onRefused,
                modifier = Modifier.weight(1f),
            )
        }
    }
}
