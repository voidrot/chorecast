package dev.voidrot.chorecast.ui.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.core.Fetch
import dev.voidrot.chorecast.core.ParentOverview
import dev.voidrot.chorecast.core.ParentReview
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.TokenStore
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class OverviewState(
    /** Loading with nothing to show yet. */
    val loading: Boolean = true,
    /** A pull-to-refresh is running. */
    val refreshing: Boolean = false,
    /** The last load failed. With [overview] present this is a banner over stale data. */
    val error: String? = null,
    val overview: ParentOverview? = null,
    /** The token was refused: sign in again. */
    val signedOut: Boolean = false,
)

/** Each kid's progress today, balance, streak and waiting reviews, for the parent home. */
class OverviewViewModel(
    private val url: ServerUrl,
    private val client: ParentReview,
    private val tokens: TokenStore,
) : ViewModel() {
    private val _state = MutableStateFlow(OverviewState())
    val state: StateFlow<OverviewState> = _state.asStateFlow()
    private var job: Job? = null

    init {
        refresh()
    }

    /** Back on screen (e.g. from the review queue): reload so the pending counts are current. */
    fun onResume() {
        if (job?.isActive != true) refresh()
    }

    /** First load, pull-to-refresh and the retry button. */
    fun refresh() {
        val token = tokens.read() ?: return _state.update { it.copy(signedOut = true) }
        job?.cancel()
        _state.update { it.copy(loading = it.overview == null, refreshing = it.overview != null) }
        job = viewModelScope.launch {
            val result = client.overview(url, token)
            if (result == Fetch.Unauthorized) tokens.clear()
            _state.update {
                val idle = it.copy(loading = false, refreshing = false)
                when (result) {
                    is Fetch.Ok -> idle.copy(overview = result.value, error = null)
                    Fetch.Unauthorized -> {
                        idle.copy(signedOut = true)
                    }
                    is Fetch.Unreachable -> idle.copy(error = result.reason)
                    is Fetch.Failed -> idle.copy(error = result.code)
                    Fetch.NotFound -> idle.copy(error = "http_404")
                }
            }
        }
    }
}
