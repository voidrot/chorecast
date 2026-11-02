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
 * current bearer token, a parent's session or the kid's device or session token. [onUnauthorized]
 * runs when the server refuses it (a parent clears the stored token; a kid's device decides itself).
 * [pageSize] is the server default unless only the unread count is wanted.
 */
class InboxViewModel(
    private val url: ServerUrl,
    private val client: InboxClient,
    private val token: () -> String?,
    private val onUnauthorized: () -> Unit = {},
    private val pageSize: Long? = null,
) : ViewModel() {
    private val _state = MutableStateFlow(InboxState())
    val state: StateFlow<InboxState> = _state.asStateFlow()
    private var job: Job? = null

    /** Bumped by every local read change, so a response that started earlier can't overwrite it. */
    private var version = 0

    init {
        refresh()
    }

    /** Back on screen: reload, unless the first load is still running. */
    fun onResume() {
        if (job?.isActive != true) refresh()
    }

    /** First load, pull-to-refresh, retry and the unread badge. Replaces the loaded pages. */
    fun refresh() {
        val tok = token() ?: return refused()
        job?.cancel()
        val started = version
        _state.update { it.copy(loading = it.feed == null, refreshing = it.feed != null, loadingMore = false) }
        job = viewModelScope.launch {
            val result = client.list(url, tok, limit = pageSize)
            _state.update {
                val idle = it.copy(loading = false, refreshing = false)
                when {
                    result is Fetch.Ok && started == version -> idle.copy(feed = result.value, error = null)
                    // Read state changed while this was in flight: keep the local state, and the next refresh catches up.
                    result is Fetch.Ok -> idle
                    else -> idle.failed(result)
                }
            }
        }
    }

    fun loadMore() {
        val current = _state.value
        val cursor = current.feed?.nextCursor ?: return
        if (current.loadingMore || current.refreshing || current.loading) return
        val tok = token() ?: return refused()
        val started = version
        _state.update { it.copy(loadingMore = true) }
        job = viewModelScope.launch {
            val result = client.list(url, tok, cursor, pageSize)
            _state.update {
                val idle = it.copy(loadingMore = false)
                val feed = it.feed
                when {
                    result is Fetch.Ok && feed != null -> {
                        val merged = feed.append(result.value)
                        // A read marked meanwhile has already lowered the count the page was counted before.
                        idle.copy(feed = if (started == version) merged else merged.copy(unread = feed.unread), error = null)
                    }
                    result is Fetch.Ok -> idle.copy(feed = result.value, error = null)
                    else -> idle.failed(result)
                }
            }
        }
    }

    /** Marks [entry] read right away. If the server doesn't confirm, the list is reloaded to show the truth. */
    fun markRead(entry: InboxEntry) {
        if (entry.read) return
        val tok = token() ?: return refused()
        version++
        _state.update { s -> s.copy(feed = s.feed?.markRead(entry.id)) }
        viewModelScope.launch {
            when (val result = client.markRead(url, tok, entry.id)) {
                is Fetch.Ok, Fetch.NotFound -> Unit // gone from the server (purged): nothing to restore
                else -> {
                    _state.update { it.failed(result) }
                    if (result != Fetch.Unauthorized) {
                        version++
                        refresh()
                    }
                }
            }
        }
    }

    fun markAllRead() {
        val tok = token() ?: return refused()
        viewModelScope.launch {
            when (val result = client.markAllRead(url, tok)) {
                is Fetch.Ok -> {
                    version++
                    _state.update { s -> s.copy(feed = s.feed?.markAllRead(), error = null) }
                }
                else -> _state.update { it.failed(result) }
            }
        }
    }

    private fun refused() {
        onUnauthorized()
        _state.update { it.copy(signedOut = true, loading = false) }
    }

    private fun InboxState.failed(result: Fetch<*>): InboxState = when (result) {
        Fetch.Unauthorized -> {
            onUnauthorized()
            copy(signedOut = true)
        }
        is Fetch.Unreachable -> copy(error = result.reason)
        is Fetch.Failed -> copy(error = result.code)
        else -> copy(error = "http_404")
    }
}
