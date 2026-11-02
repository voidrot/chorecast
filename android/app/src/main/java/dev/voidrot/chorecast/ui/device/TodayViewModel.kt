package dev.voidrot.chorecast.ui.device

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.api.models.Chore
import dev.voidrot.chorecast.core.Fetch
import dev.voidrot.chorecast.core.KidChores
import dev.voidrot.chorecast.core.KidToday
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.TodayChore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

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
    val choreFailed: Boolean = false,
) {
    val selectedChore: TodayChore? get() = selected?.let { today?.plan?.find(it) }

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
    private val kidId: Long,
    private val token: String,
) : ViewModel() {
    private val _state = MutableStateFlow(TodayState())
    val state: StateFlow<TodayState> = _state.asStateFlow()

    init {
        refresh()
    }

    /** First load, pull-to-refresh and the retry button. With nothing loaded yet it shows the spinner. */
    fun refresh() {
        _state.update { it.copy(loading = it.today == null, refreshing = it.today != null) }
        viewModelScope.launch {
            val result = client.today(url, token, kidId)
            _state.update {
                val idle = it.copy(loading = false, refreshing = false)
                when (result) {
                    is Fetch.Ok -> idle.copy(today = result.value, error = null)
                    Fetch.Unauthorized -> idle.copy(error = TodayError.SignedOut)
                    is Fetch.Unreachable -> idle.copy(error = TodayError.Offline(result.reason))
                    is Fetch.Failed -> idle.copy(error = TodayError.Failed(result.code))
                    Fetch.NotFound -> idle.copy(error = TodayError.Failed("http_404"))
                }
            }
        }
    }

    /** Opens a chore's details, reading the chore again so the steps and evidence rules are current. */
    fun open(instanceId: Long) {
        val choreId = _state.value.today?.plan?.find(instanceId)?.instance?.choreId ?: return
        _state.update { it.copy(selected = instanceId, choreFailed = false) }
        viewModelScope.launch {
            when (val result = client.chore(url, token, choreId)) {
                is Fetch.Ok -> _state.update { it.copy(fetchedChores = it.fetchedChores + (choreId to result.value)) }
                else -> _state.update { it.copy(choreFailed = true) }
            }
        }
    }

    fun close() = _state.update { it.copy(selected = null) }
}
