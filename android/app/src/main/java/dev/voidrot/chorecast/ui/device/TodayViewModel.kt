package dev.voidrot.chorecast.ui.device

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.api.models.Chore
import dev.voidrot.chorecast.core.CapturedPhoto
import dev.voidrot.chorecast.core.ChoreCompleter
import dev.voidrot.chorecast.core.CompleteError
import dev.voidrot.chorecast.core.CompletionDraft
import dev.voidrot.chorecast.core.CompletionNeeds
import dev.voidrot.chorecast.core.Fetch
import dev.voidrot.chorecast.core.KidChores
import dev.voidrot.chorecast.core.KidToday
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.data.FileCaptureStore
import dev.voidrot.chorecast.core.PhotoProblem
import dev.voidrot.chorecast.core.SubmitOutcome
import dev.voidrot.chorecast.core.TodayChore
import dev.voidrot.chorecast.core.completionNeeds
import dev.voidrot.chorecast.core.isTodayStale
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant

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
    /** Chores read on demand when the list didn't have them. */
    val fetchedChores: Map<Long, Chore> = emptyMap(),
    /** The chore whose last read failed, so a failure doesn't carry over to another chore. */
    val failedChoreId: Long? = null,
    /** What the kid has put together for each instance, kept until it is accepted. */
    val drafts: Map<Long, CompletionDraft> = emptyMap(),
    /** The complete form is open for the selected chore. */
    val completing: Boolean = false,
    /** The camera is open for the selected chore. */
    val cameraOpen: Boolean = false,
    val submitting: Boolean = false,
    /** Why the last submit failed. Cleared by the next submit or by [TodayViewModel.dismissCompleteError]. */
    val completeError: CompleteError? = null,
) {
    val selectedDraft: CompletionDraft? get() = selected?.let { drafts[it] }

    /** What the selected chore asks the kid to hand in. Null until the chore has been read. */
    val selectedNeeds: CompletionNeeds? get() = selectedChoreDetails?.completionNeeds()

    val selectedChore: TodayChore? get() = selected?.let { today?.plan?.find(it) }

    val choreFailed: Boolean
        get() = failedChoreId != null && failedChoreId == selectedChore?.instance?.choreId

    /** The open instance's chore: steps, evidence rules, description. Null until it has been read. */
    val selectedChoreDetails: Chore?
        get() = selectedChore?.instance?.choreId?.let { fetchedChores[it] ?: today?.chores?.get(it) }
}

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
    private val now: () -> Instant = Instant::now,
) : ViewModel() {
    private val _state = MutableStateFlow(TodayState())
    val state: StateFlow<TodayState> = _state.asStateFlow()
    private var refreshJob: Job? = null
    private var loadedAt: Instant? = null

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

    /** Opens a chore's details, reading the chore again so the steps and evidence rules are current. */
    fun open(instanceId: Long) {
        val choreId = _state.value.today?.plan?.find(instanceId)?.instance?.choreId ?: return
        _state.update {
            it.copy(selected = instanceId, failedChoreId = null, completing = false, cameraOpen = false, completeError = null)
        }
        viewModelScope.launch {
            when (val result = client.chore(url, token, choreId)) {
                is Fetch.Ok -> _state.update {
                    it.copy(
                        fetchedChores = it.fetchedChores + (choreId to result.value),
                        failedChoreId = it.failedChoreId.takeIf { failed -> failed != choreId },
                    )
                }
                else -> _state.update { it.copy(failedChoreId = choreId) }
            }
        }
    }

    fun close() = _state.update { it.copy(selected = null, completing = false, cameraOpen = false, completeError = null) }

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
        if (_state.value.submitting) return
        captures.delete(captureId)
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
                    _state.update { st -> st.copy(drafts = st.drafts - id, completing = false, selected = null) }
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
                _state.value.drafts[id]?.photos?.forEach { captures.delete(it.captureId) }
                _state.update { it.copy(drafts = it.drafts - id, completing = false, cameraOpen = false, completeError = error) }
                refresh()
            }
            // A photo the server will never take: drop it so the kid takes another.
            is CompleteError.BadPhoto -> {
                captures.delete(error.captureId)
                editDraft(id) { it.removePhoto(error.captureId) }
                _state.update { it.copy(completeError = error) }
            }
            else -> _state.update { it.copy(completeError = error) }
        }
    }

    /** A finished session drops unsent photos from the device. */
    override fun onCleared() {
        _state.value.drafts.values.flatMap { it.photos }.forEach { captures.delete(it.captureId) }
    }
}
