package dev.voidrot.chorecast.ui.inbox

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.core.Fetch
import dev.voidrot.chorecast.core.InboxClient
import dev.voidrot.chorecast.core.InboxEntry
import dev.voidrot.chorecast.core.InboxFeed
import dev.voidrot.chorecast.core.ServerUrl
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class InboxState(
    /** Loading with nothing to show yet. */
    val loading: Boolean = true,
    /** A pull-to-refresh is running. */
    val refreshing: Boolean = false,
    val loadingMore: Boolean = false,
    /** The last load failed. With [feed] present this is a banner over stale data. */
    val error: String? = null,
    val feed: InboxFeed? = null,
    /** The token was refused. */
    val signedOut: Boolean = false,
) {
    val unread: Long get() = feed?.unread ?: 0
}

/**
 * The caller's inbox: first page, pull to refresh, load more, mark read. [token] returns the
 * current bearer token, a parent's session or the kid's device or session token.
 */
class InboxViewModel(
    private val url: ServerUrl,
    private val client: InboxClient,
    private val token: () -> String?,
) : ViewModel() {
    private val _state = MutableStateFlow(InboxState())
    val state: StateFlow<InboxState> = _state.asStateFlow()
    private var job: Job? = null

    init {
        refresh()
    }

    /** First load, pull-to-refresh, retry, and the unread badge when its screen comes back. Replaces the loaded pages. */
    fun refresh() {
        val tok = token() ?: return _state.update { it.copy(signedOut = true, loading = false) }
        job?.cancel()
        _state.update { it.copy(loading = it.feed == null, refreshing = it.feed != null, loadingMore = false) }
        job = viewModelScope.launch {
            val result = client.list(url, tok)
            _state.update {
                val idle = it.copy(loading = false, refreshing = false)
                when (result) {
                    is Fetch.Ok -> idle.copy(feed = result.value, error = null)
                    else -> idle.failed(result)
                }
            }
        }
    }

    fun loadMore() {
        val current = _state.value
        val cursor = current.feed?.nextCursor ?: return
        if (current.loadingMore || current.refreshing || current.loading) return
        val tok = token() ?: return _state.update { it.copy(signedOut = true) }
        _state.update { it.copy(loadingMore = true) }
        job = viewModelScope.launch {
            val result = client.list(url, tok, cursor)
            _state.update {
                val idle = it.copy(loadingMore = false)
                when (result) {
                    is Fetch.Ok -> idle.copy(feed = it.feed?.append(result.value) ?: result.value, error = null)
                    else -> idle.failed(result)
                }
            }
        }
    }

    /** Marks [entry] read right away; the server call is best effort, and a refresh restores the truth if it failed. */
    fun markRead(entry: InboxEntry) {
        if (entry.read) return
        val tok = token() ?: return _state.update { it.copy(signedOut = true) }
        _state.update { s -> s.copy(feed = s.feed?.markRead(entry.id)) }
        viewModelScope.launch {
            if (client.markRead(url, tok, entry.id) == Fetch.Unauthorized) _state.update { it.copy(signedOut = true) }
        }
    }

    fun markAllRead() {
        val tok = token() ?: return _state.update { it.copy(signedOut = true) }
        viewModelScope.launch {
            when (val result = client.markAllRead(url, tok)) {
                is Fetch.Ok -> _state.update { s -> s.copy(feed = s.feed?.markAllRead(), error = null) }
                else -> _state.update { it.failed(result) }
            }
        }
    }

    private fun InboxState.failed(result: Fetch<*>): InboxState = when (result) {
        Fetch.Unauthorized -> copy(signedOut = true)
        is Fetch.Unreachable -> copy(error = result.reason)
        is Fetch.Failed -> copy(error = result.code)
        else -> copy(error = "http_404")
    }
}
