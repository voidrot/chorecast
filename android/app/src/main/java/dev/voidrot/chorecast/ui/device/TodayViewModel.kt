package dev.voidrot.chorecast.ui.device

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.core.CapturedPhoto
import dev.voidrot.chorecast.core.ChoreCompleter
import dev.voidrot.chorecast.core.CompleteError
import dev.voidrot.chorecast.core.CompletionDraft
import dev.voidrot.chorecast.core.CompletionNeeds
import dev.voidrot.chorecast.core.EvidenceView
import dev.voidrot.chorecast.core.Fetch
import dev.voidrot.chorecast.core.KidChores
import dev.voidrot.chorecast.core.KidHistory
import dev.voidrot.chorecast.core.KidToday
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.data.FileCaptureStore
import dev.voidrot.chorecast.core.PhotoProblem
import dev.voidrot.chorecast.core.SubmitOutcome
import dev.voidrot.chorecast.core.TodayChore
import dev.voidrot.chorecast.core.completionNeeds
import dev.voidrot.chorecast.core.firstHistory
import dev.voidrot.chorecast.core.latestHistoryRange
import dev.voidrot.chorecast.core.latestView
import dev.voidrot.chorecast.core.isTodayStale
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.LocalDate

sealed interface TodayError {
    data class Offline(val reason: String) : TodayError

    /** The kid session or device token was refused. */
    data object SignedOut : TodayError
    data class Failed(val code: String) : TodayError
}

data class TodayState(
    /** Loading with nothing to show yet. */
    val loading: Boolean = true,
    /** A pull-to-refresh is running. */
    val refreshing: Boolean = false,
    /** The last load failed. With [today] present this is a banner over stale data. */
    val error: TodayError? = null,
    val today: KidToday? = null,
    /** The instance whose details are open. */
    val selected: Long? = null,
    /** What the kid has put together for each instance, kept until it is accepted. */
    val drafts: Map<Long, CompletionDraft> = emptyMap(),
    /** The complete form is open for the selected chore. */
    val completing: Boolean = false,
    /** The camera is open for the selected chore. */
    val cameraOpen: Boolean = false,
    val submitting: Boolean = false,
    /** Why the last submit failed. Cleared by the next submit or by [TodayViewModel.dismissCompleteError]. */
    val completeError: CompleteError? = null,
    val history: HistoryState = HistoryState(),
) {
    val selectedDraft: CompletionDraft? get() = selected?.let { drafts[it] }

    /** What the selected chore asks the kid to hand in; the instance carries the chore's steps and rules. */
    val selectedNeeds: CompletionNeeds? get() = selectedChore?.instance?.completionNeeds()

    val selectedChore: TodayChore? get() = selected?.let { today?.plan?.find(it) }
}

/** What opening a past chore shows. The photo links are signed and short-lived, so they are read on every open. */
sealed interface EvidenceLoad {
    data object Loading : EvidenceLoad

    /** [view] is null when nothing was submitted (a missed or excused chore). */
    data class Loaded(val view: EvidenceView?) : EvidenceLoad
    data class Failed(val error: TodayError) : EvidenceLoad
}

/** The kid's history screen: finished chores back in time, and the one opened for its evidence. */
data class HistoryState(
    val open: Boolean = false,
    /** Null until the first page has loaded. */
    val history: KidHistory? = null,
    val loading: Boolean = false,
    /** The last page failed; with [history] present the loaded pages stay visible. */
    val error: TodayError? = null,
    val selected: ChoreInstance? = null,
    val evidence: EvidenceLoad = EvidenceLoad.Loading,
)

/**
 * A kid's today list, points and streak, and the chore details opened from it. One per kid session
 * ([token] is that session's, or a kid-bound device's own).
 */
class TodayViewModel(
    private val url: ServerUrl,
    private val client: KidChores,
    private val completer: ChoreCompleter,
    private val captures: FileCaptureStore,
    private val kidId: Long,
    private val token: String,
    private val onCompleted: () -> Unit = {},
    private val now: () -> Instant = Instant::now,
    private val today: () -> LocalDate = LocalDate::now,
) : ViewModel() {
    private val _state = MutableStateFlow(TodayState())
    val state: StateFlow<TodayState> = _state.asStateFlow()
    private var refreshJob: Job? = null
    private var loadedAt: Instant? = null
    private var historyJob: Job? = null
    private var evidenceJob: Job? = null

    init {
        captures.clearAll()
        refresh()
    }

    /** First load, pull-to-refresh and the retry button. With nothing loaded yet it shows the spinner. */
    fun refresh() {
        refreshJob?.cancel()
        _state.update { it.copy(loading = it.today == null, refreshing = it.today != null) }
        refreshJob = viewModelScope.launch {
            val result = client.today(url, token, kidId)
            if (result is Fetch.Ok) loadedAt = now()
            _state.update {
                val idle = it.copy(loading = false, refreshing = false)
                when (result) {
                    // The open chore may be gone from the new list (a new day): fall back to the list.
                    is Fetch.Ok -> idle.copy(
                        today = result.value,
                        error = null,
                        selected = it.selected?.takeIf { id -> result.value.plan.find(id) != null },
                    )
                    Fetch.Unauthorized -> idle.copy(error = TodayError.SignedOut)
                    is Fetch.Unreachable -> idle.copy(error = TodayError.Offline(result.reason))
                    is Fetch.Failed -> idle.copy(error = TodayError.Failed(result.code))
                    Fetch.NotFound -> idle.copy(error = TodayError.Failed("http_404"))
                }
            }
        }
    }

    /**
     * Called when the screen comes back to the foreground. A phone left open overnight would
     * otherwise show yesterday's chores; a load that is running or recent enough is left alone.
     */
    fun refreshIfStale() {
        if (refreshJob?.isActive == true) return
        if (isTodayStale(loadedAt, now())) refresh()
    }

    /**
     * Opens a chore's details. The instance already carries its steps and evidence rules.
     * Returns false, and opens nothing, when the instance isn't on the loaded list.
     */
    fun open(instanceId: Long): Boolean {
        if (_state.value.today?.plan?.find(instanceId) == null) return false
        _state.update { it.copy(selected = instanceId, completing = false, cameraOpen = false, completeError = null) }
        return true
    }

    fun close() = _state.update { it.copy(selected = null, completing = false, cameraOpen = false, completeError = null) }

    // History: finished chores, paged backwards by date range.

    fun openHistory() {
        _state.update { it.copy(history = it.history.copy(open = true)) }
        refreshHistory()
    }

    fun closeHistory() {
        evidenceJob?.cancel()
        _state.update { it.copy(history = it.history.copy(open = false, selected = null)) }
    }

    /** Retry after a failed first page. */
    fun retryHistory() = refreshHistory()

    /**
     * Reloads the newest page, replacing what was loaded (paging starts over). The old list stays
     * visible while it loads, and stays if the load fails.
     */
    fun refreshHistory() = fetchHistory(older = false)

    /** Loads the page before the oldest loaded one. A load that is running is left alone. */
    fun loadHistory() = fetchHistory(older = true)

    private fun fetchHistory(older: Boolean) {
        if (older && historyJob?.isActive == true) return
        historyJob?.cancel()
        val loaded = _state.value.history.history.takeIf { older }
        val range = loaded?.oldest?.older() ?: latestHistoryRange(today())
        _state.update { it.copy(history = it.history.copy(loading = true, error = null)) }
        historyJob = viewModelScope.launch {
            val result = client.instancesIn(url, token, kidId, range)
            _state.update { st ->
                val h = st.history.copy(loading = false)
                st.copy(
                    history = when (result) {
                        is Fetch.Ok -> h.copy(history = loaded?.withOlder(range, result.value) ?: firstHistory(range, result.value))
                        else -> h.copy(error = result.toTodayError())
                    },
                )
            }
        }
    }

    /** Opens a past chore and reads its evidence again, since the photo links expire. */
    fun openHistoryChore(instance: ChoreInstance) {
        evidenceJob?.cancel()
        _state.update { it.copy(history = it.history.copy(selected = instance, evidence = EvidenceLoad.Loading)) }
        evidenceJob = viewModelScope.launch {
            val result = client.evidence(url, token, instance.id)
            val load = when (result) {
                is Fetch.Ok -> EvidenceLoad.Loaded(result.value.latestView(instance.steps.orEmpty()))
                else -> EvidenceLoad.Failed(result.toTodayError())
            }
            _state.update { it.copy(history = it.history.copy(evidence = load)) }
        }
    }

    fun closeHistoryChore() {
        evidenceJob?.cancel()
        _state.update { it.copy(history = it.history.copy(selected = null)) }
    }

    private fun Fetch<*>.toTodayError(): TodayError = when (this) {
        Fetch.Unauthorized -> TodayError.SignedOut
        is Fetch.Unreachable -> TodayError.Offline(reason)
        is Fetch.Failed -> TodayError.Failed(code)
        Fetch.NotFound -> TodayError.Failed("http_404")
        is Fetch.Ok -> error("not an error")
    }

    // The complete flow. Drafts live here, in the session's ViewModelStore, so a rotation keeps them.

    fun beginComplete() {
        val id = _state.value.selected ?: return
        _state.update { it.copy(completing = true, completeError = null, drafts = it.drafts + (id to (it.drafts[id] ?: CompletionDraft(id)))) }
    }

    fun cancelComplete() = _state.update { it.copy(completing = false, cameraOpen = false) }

    fun toggleStep(index: Int) = userEdit { it.toggleStep(index) }

    fun setNote(text: String) = userEdit { it.withNote(text) }

    fun openCamera() = _state.update { it.copy(cameraOpen = true) }

    fun closeCamera() = _state.update { it.copy(cameraOpen = false) }

    /** Where the camera should write the next photo, and the id that names it. */
    fun newCaptureFile(captureId: String) = captures.fileFor(captureId)

    /** The shutter fired and the file is written. [photo] carries the time and id taken at the shutter. */
    fun onCaptured(photo: CapturedPhoto) {
        userEdit { it.addPhoto(photo) }
        _state.update { it.copy(cameraOpen = false) }
    }

    /** The shutter fired but saving failed: nothing to keep. */
    fun onCaptureFailed(captureId: String) {
        captures.delete(captureId)
        _state.update { it.copy(cameraOpen = false, completeError = CompleteError.BadPhoto(captureId, PhotoProblem.LOST)) }
    }

    fun removePhoto(captureId: String) {
        val s = _state.value
        if (s.submitting) return
        captures.delete(captureId)
        s.selectedDraft?.let { completer.discardRemoved(url, token, it, captureId) }
        userEdit { it.removePhoto(captureId) }
    }

    fun dismissCompleteError() = _state.update { it.copy(completeError = null) }

    /** An edit by the kid, to the open chore's draft. Ignored while a submit is running. */
    private fun userEdit(change: (CompletionDraft) -> CompletionDraft) {
        val s = _state.value
        if (s.submitting) return
        s.selected?.let { editDraft(it, change) }
    }

    private fun editDraft(id: Long, change: (CompletionDraft) -> CompletionDraft) {
        _state.update { s -> s.copy(drafts = s.drafts + (id to change(s.drafts[id] ?: CompletionDraft(id)))) }
    }

    /** Uploads the photos and completes the chore. A second tap while one is running does nothing. */
    fun submit() {
        val s = _state.value
        val id = s.selected ?: return
        val needs = s.selectedNeeds ?: return
        val draft = s.drafts[id] ?: return
        if (s.submitting) return
        _state.update { it.copy(submitting = true, completeError = null) }
        viewModelScope.launch {
            val outcome = completer.submit(url, token, needs, draft)
            _state.update { st -> st.copy(submitting = false, drafts = st.drafts + (id to outcome.draft)) }
            when (val r = outcome.result) {
                is SubmitOutcome.Result.Completed -> {
                    // The new submission belongs in history: drop what was loaded so the next open reads it.
                    historyJob?.cancel()
                    _state.update { st ->
                        st.copy(drafts = st.drafts - id, completing = false, selected = null, history = HistoryState())
                    }
                    onCompleted()
                    refresh()
                }
                is SubmitOutcome.Result.Failed -> failed(id, r.error)
            }
        }
    }

    private fun failed(id: Long, error: CompleteError) {
        when (error) {
            CompleteError.SignedOut -> _state.update { it.copy(error = TodayError.SignedOut) }
            // The chore moved on without this submit: its draft and photos are no use, show what it is now.
            CompleteError.Closed, CompleteError.NotOpen -> {
                _state.value.drafts[id]?.let { dropDraft(it) }
                _state.update { it.copy(drafts = it.drafts - id, completing = false, cameraOpen = false, completeError = error) }
                refresh()
            }
            // A photo the server will never take: drop it so the kid takes another.
            is CompleteError.BadPhoto -> {
                captures.delete(error.captureId)
                _state.value.drafts[id]?.let { completer.discardRemoved(url, token, it, error.captureId) }
                editDraft(id) { it.removePhoto(error.captureId) }
                _state.update { it.copy(completeError = error) }
            }
            else -> _state.update { it.copy(completeError = error) }
        }
    }

    /** Deletes the draft's photo files and frees its uploads on the server. */
    private fun dropDraft(draft: CompletionDraft) {
        draft.photos.forEach { captures.delete(it.captureId) }
        completer.discardDropped(url, token, draft)
    }

    /** A finished session drops unsent photos from the device and the server. */
    override fun onCleared() {
        _state.value.drafts.values.forEach(::dropDraft)
    }
}
