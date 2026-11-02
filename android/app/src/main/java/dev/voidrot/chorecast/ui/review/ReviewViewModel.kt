package dev.voidrot.chorecast.ui.review

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.api.models.RejectChoreInstanceInputBody
import dev.voidrot.chorecast.api.models.ReviewQueueItem
import dev.voidrot.chorecast.core.Decision
import dev.voidrot.chorecast.core.Fetch
import dev.voidrot.chorecast.core.ParentReview
import dev.voidrot.chorecast.core.REJECT_TEXT_MAX
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.TokenStore
import dev.voidrot.chorecast.core.buildRejection
import dev.voidrot.chorecast.core.groupReviewByKid
import dev.voidrot.chorecast.core.isQueueStale
import dev.voidrot.chorecast.core.mergeReviewPage
import dev.voidrot.chorecast.core.pruneSelection
import dev.voidrot.chorecast.core.removeReviewItems
import dev.voidrot.chorecast.core.takeCodePoints
import dev.voidrot.chorecast.core.toggleSelected
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant

/** A short message shown once after an action. */
sealed interface ReviewNotice {
    data class Approved(val count: Int) : ReviewNotice

    /** Someone else decided first; the chore left the list. */
    data class AlreadyDecided(val count: Int) : ReviewNotice
    data object Rejected : ReviewNotice
    data class SomeFailed(val count: Int) : ReviewNotice
    data class Offline(val reason: String) : ReviewNotice
    data class Failed(val code: String) : ReviewNotice
}

/** The reject dialog's draft. */
data class RejectDraft(
    val item: ReviewQueueItem,
    val reason: RejectChoreInstanceInputBody.Reason? = null,
    val text: String = "",
)

data class ReviewState(
    val loading: Boolean = true,
    val refreshing: Boolean = false,
    val loadingMore: Boolean = false,
    /** The last load failed. With [items] present this is a banner over stale data. */
    val error: String? = null,
    val items: List<ReviewQueueItem> = emptyList(),
    val nextCursor: String? = null,
    val selected: Set<Long> = emptySet(),
    /** An approval or rejection is running; the buttons wait for it. */
    val working: Boolean = false,
    val reject: RejectDraft? = null,
    val notice: ReviewNotice? = null,
    val signedOut: Boolean = false,
) {
    val groups get() = groupReviewByKid(items)
    val canSubmitReject: Boolean get() = reject?.let { buildRejection(it.reason, it.text) } != null
}

/** The waiting submissions: approve, bulk approve and reject with a reason and feedback. */
class ReviewViewModel(
    private val url: ServerUrl,
    private val client: ParentReview,
    private val tokens: TokenStore,
    private val now: () -> Instant = Instant::now,
) : ViewModel() {
    private val _state = MutableStateFlow(ReviewState())
    val state: StateFlow<ReviewState> = _state.asStateFlow()
    private var loadJob: Job? = null
    private var loadedAt: Instant? = null

    init {
        refresh()
    }

    private fun token(): String? = tokens.read().also { if (it == null) _state.update { s -> s.copy(signedOut = true) } }

    /** First load, pull-to-refresh and retry: the first page again, so signed photo links are fresh. */
    fun refresh() {
        if (_state.value.working) return
        val token = token() ?: return
        loadJob?.cancel()
        _state.update { it.copy(loading = it.items.isEmpty(), refreshing = it.items.isNotEmpty(), loadingMore = false) }
        loadJob = viewModelScope.launch {
            val result = client.queue(url, token)
            if (result == Fetch.Unauthorized) tokens.clear()
            if (result is Fetch.Ok) loadedAt = now()
            _state.update {
                val idle = it.copy(loading = false, refreshing = false)
                when (result) {
                    is Fetch.Ok -> idle.copy(
                        items = result.value.items,
                        nextCursor = result.value.nextCursor,
                        selected = pruneSelection(it.selected, result.value.items),
                        error = null,
                    )
                    else -> failed(idle, result)
                }
            }
        }
    }

    /** Reloads when the queue is older than the signed photo links last. */
    fun refreshIfStale() {
        if (loadJob?.isActive == true || _state.value.working) return
        if (isQueueStale(loadedAt, now())) refresh()
    }

    fun loadMore() {
        val s = _state.value
        val cursor = s.nextCursor ?: return
        if (s.loadingMore || s.loading || s.refreshing || s.working) return
        val token = token() ?: return
        _state.update { it.copy(loadingMore = true) }
        loadJob = viewModelScope.launch {
            val result = client.queue(url, token, cursor)
            if (result == Fetch.Unauthorized) tokens.clear()
            _state.update {
                val idle = it.copy(loadingMore = false)
                when (result) {
                    is Fetch.Ok -> idle.copy(
                        items = mergeReviewPage(it.items, result.value.items),
                        nextCursor = result.value.nextCursor,
                    )
                    else -> failed(idle, result)
                }
            }
        }
    }

    private fun failed(s: ReviewState, result: Fetch<*>): ReviewState = when (result) {
        Fetch.Unauthorized -> {
            s.copy(signedOut = true)
        }
        is Fetch.Unreachable -> s.copy(error = result.reason)
        is Fetch.Failed -> s.copy(error = result.code)
        else -> s.copy(error = "http_404")
    }

    fun toggle(id: Long) = _state.update { it.copy(selected = toggleSelected(it.selected, id)) }

    fun selectAll() = _state.update {
        val all = it.items.mapTo(LinkedHashSet()) { item -> item.instance.id }
        it.copy(selected = if (it.selected == all) emptySet() else all)
    }

    fun approve(id: Long) {
        if (_state.value.working) return
        val token = token() ?: return
        _state.update { it.copy(working = true) }
        viewModelScope.launch {
            val decision = client.approve(url, token, id)
            if (decision == Decision.Unauthorized) tokens.clear()
            _state.update { afterDecision(it, decision, setOf(id), ReviewNotice.Approved(1)) }
        }
    }

    fun approveSelected() {
        val s = _state.value
        if (s.working || s.selected.isEmpty()) return
        val token = token() ?: return
        val ids = s.items.map { it.instance.id }.filter { it in s.selected }
        _state.update { it.copy(working = true) }
        viewModelScope.launch {
            val out = client.approveAll(url, token, ids)
            if (out.stopped == Decision.Unauthorized) tokens.clear()
            _state.update {
                val gone = out.summary.removable
                val base = it.copy(
                    working = false,
                    items = removeReviewItems(it.items, gone),
                    selected = it.selected - gone,
                )
                when (val stopped = out.stopped) {
                    null -> base.copy(notice = bulkNotice(out.summary.approved.size, out.summary.alreadyDecided.size, out.summary.failed.size))
                    Decision.Unauthorized -> {
                        base.copy(signedOut = true)
                    }
                    is Decision.Unreachable -> base.copy(notice = ReviewNotice.Offline(stopped.reason))
                    else -> base.copy(notice = ReviewNotice.Failed(stopped.errorCode()))
                }
            }
        }
    }

    private fun bulkNotice(approved: Int, decided: Int, failed: Int): ReviewNotice = when {
        failed > 0 -> ReviewNotice.SomeFailed(failed)
        approved == 0 && decided > 0 -> ReviewNotice.AlreadyDecided(decided)
        else -> ReviewNotice.Approved(approved)
    }

    fun openReject(item: ReviewQueueItem) = _state.update { it.copy(reject = RejectDraft(item)) }

    fun closeReject() = _state.update { it.copy(reject = null) }

    fun setReason(reason: RejectChoreInstanceInputBody.Reason) = _state.update { s ->
        s.copy(reject = s.reject?.let { it.copy(reason = if (it.reason == reason) null else reason) })
    }

    fun setText(text: String) = _state.update { s -> s.copy(reject = s.reject?.copy(text = text.takeCodePoints(REJECT_TEXT_MAX))) }

    fun confirmReject() {
        val s = _state.value
        val draft = s.reject ?: return
        val body = buildRejection(draft.reason, draft.text) ?: return
        if (s.working) return
        val token = token() ?: return
        val id = draft.item.instance.id
        _state.update { it.copy(working = true) }
        viewModelScope.launch {
            val decision = client.reject(url, token, id, body)
            if (decision == Decision.Unauthorized) tokens.clear()
            _state.update { afterDecision(it, decision, setOf(id), ReviewNotice.Rejected) }
        }
    }

    private fun afterDecision(s: ReviewState, decision: Decision, ids: Set<Long>, done: ReviewNotice): ReviewState {
        val left = s.copy(working = false)
        val removed = left.copy(
            items = removeReviewItems(s.items, ids),
            selected = s.selected - ids,
            reject = null,
        )
        return when (decision) {
            Decision.Done -> removed.copy(notice = done)
            Decision.AlreadyDecided -> removed.copy(notice = ReviewNotice.AlreadyDecided(1))
            Decision.Unauthorized -> {
                left.copy(signedOut = true)
            }
            is Decision.Unreachable -> left.copy(notice = ReviewNotice.Offline(decision.reason))
            // Forbidden, Invalid and server errors: keep the dialog and the chore so it can be retried.
            Decision.Forbidden, is Decision.Invalid, is Decision.Failed ->
                left.copy(notice = ReviewNotice.Failed(decision.errorCode()))
        }
    }

    fun noticeShown() = _state.update { it.copy(notice = null) }
}

private fun Decision.errorCode(): String = when (this) {
    Decision.Forbidden -> "http_403"
    is Decision.Invalid -> code
    is Decision.Failed -> code
    is Decision.Unreachable -> reason
    else -> "error"
}
