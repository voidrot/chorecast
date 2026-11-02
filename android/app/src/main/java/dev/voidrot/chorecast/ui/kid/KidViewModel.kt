package dev.voidrot.chorecast.ui.kid

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.Kid
import dev.voidrot.chorecast.core.ActionResult
import dev.voidrot.chorecast.core.AdjustmentCheck
import dev.voidrot.chorecast.core.AdjustmentProblem
import dev.voidrot.chorecast.core.Fetch
import dev.voidrot.chorecast.core.HouseholdInfo
import dev.voidrot.chorecast.core.KID_DAY_LOOKBACK
import dev.voidrot.chorecast.core.KidDayData
import dev.voidrot.chorecast.core.KidDayPlan
import dev.voidrot.chorecast.core.NOTE_MAX
import dev.voidrot.chorecast.core.ParentReview
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.StaleReason
import dev.voidrot.chorecast.core.TokenStore
import dev.voidrot.chorecast.core.buildActionNote
import dev.voidrot.chorecast.core.buildAdjustment
import dev.voidrot.chorecast.core.householdToday
import dev.voidrot.chorecast.core.planKidDay
import dev.voidrot.chorecast.core.staleReasonFor
import dev.voidrot.chorecast.core.takeCodePoints
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId

/** The form or confirmation that is open over the kid's screen. */
sealed interface KidDialog {
    data class Adjust(
        val subtract: Boolean = false,
        val amount: String = "",
        val note: String = "",
        val problem: AdjustmentProblem? = null,
    ) : KidDialog

    /** Excuse a missed chore or reverse an approval, each with an optional note. */
    data class Excuse(val instance: ChoreInstance, val note: String = "") : KidDialog
    data class Reverse(val instance: ChoreInstance, val note: String = "") : KidDialog
    data class Reassign(val instance: ChoreInstance) : KidDialog
}

/** A short message shown once after an action. */
sealed interface KidNotice {
    data object Adjusted : KidNotice
    data object Excused : KidNotice
    data object Reversed : KidNotice
    data object Reassigned : KidNotice

    /** The chore had changed; the list was reloaded. */
    data class Stale(val reason: StaleReason) : KidNotice
    data class Offline(val reason: String) : KidNotice
    data class Failed(val code: String) : KidNotice
}

data class KidState(
    val loading: Boolean = true,
    val refreshing: Boolean = false,
    /** The last load failed. With [data] present this is a banner over stale data. */
    val error: String? = null,
    val data: KidDayData? = null,
    val plan: KidDayPlan? = null,
    /** A write is running; the buttons wait for it. */
    val working: Boolean = false,
    val dialog: KidDialog? = null,
    val notice: KidNotice? = null,
    val signedOut: Boolean = false,
)

/** One kid for the parent: the recent chores with their actions, and manual point adjustments. */
class KidViewModel(
    private val url: ServerUrl,
    private val kidId: Long,
    private val client: ParentReview,
    private val household: HouseholdInfo,
    private val tokens: TokenStore,
    private val now: () -> Instant = Instant::now,
) : ViewModel() {
    private val _state = MutableStateFlow(KidState())
    val state: StateFlow<KidState> = _state.asStateFlow()
    private var job: Job? = null

    init {
        refresh()
    }

    private fun token(): String? = tokens.read().also { if (it == null) _state.update { s -> s.copy(signedOut = true) } }

    /** First load, pull-to-refresh, retry and the reload after a write. */
    fun refresh() {
        val token = token() ?: return
        job?.cancel()
        _state.update { it.copy(loading = it.data == null, refreshing = it.data != null) }
        job = viewModelScope.launch {
            val settings = household.get(url, token)
            val zone = settings?.let { runCatching { ZoneId.of(it.timezone) }.getOrNull() } ?: ZoneId.systemDefault()
            val today = householdToday(zone, settings?.dayEnd ?: "00:00", now())
            val result = client.kidDay(url, token, kidId, today.minusDays(KID_DAY_LOOKBACK), today)
            if (result == Fetch.Unauthorized) tokens.clear()
            _state.update {
                val idle = it.copy(loading = false, refreshing = false)
                when (result) {
                    is Fetch.Ok -> idle.copy(data = result.value, plan = planKidDay(kidId, result.value.instances, today), error = null)
                    Fetch.Unauthorized -> idle.copy(signedOut = true)
                    is Fetch.Unreachable -> idle.copy(error = result.reason)
                    is Fetch.Failed -> idle.copy(error = result.code)
                    Fetch.NotFound -> idle.copy(error = "http_404")
                }
            }
        }
    }

    fun openAdjust() = _state.update { it.copy(dialog = KidDialog.Adjust()) }

    fun openExcuse(i: ChoreInstance) = _state.update { it.copy(dialog = KidDialog.Excuse(i)) }

    fun openReverse(i: ChoreInstance) = _state.update { it.copy(dialog = KidDialog.Reverse(i)) }

    fun openReassign(i: ChoreInstance) = _state.update { it.copy(dialog = KidDialog.Reassign(i)) }

    fun closeDialog() = _state.update { it.copy(dialog = null) }

    fun setSubtract(subtract: Boolean) = editAdjust { it.copy(subtract = subtract) }

    fun setAmount(text: String) = editAdjust { it.copy(amount = text.filter { c -> c in '0'..'9' }.take(7), problem = null) }

    fun setAdjustNote(text: String) = editAdjust { it.copy(note = text.takeCodePoints(NOTE_MAX), problem = null) }

    fun setNote(text: String) = _state.update { s ->
        val capped = text.takeCodePoints(NOTE_MAX)
        s.copy(
            dialog = when (val d = s.dialog) {
                is KidDialog.Excuse -> d.copy(note = capped)
                is KidDialog.Reverse -> d.copy(note = capped)
                else -> d
            },
        )
    }

    private fun editAdjust(change: (KidDialog.Adjust) -> KidDialog.Adjust) = _state.update { s ->
        val d = s.dialog
        if (d is KidDialog.Adjust) s.copy(dialog = change(d)) else s
    }

    fun submitAdjust() {
        val s = _state.value
        val d = s.dialog as? KidDialog.Adjust ?: return
        if (s.working) return
        val body = when (val check = buildAdjustment(d.subtract, d.amount, d.note)) {
            is AdjustmentCheck.Invalid -> return editAdjust { it.copy(problem = check.problem) }
            is AdjustmentCheck.Valid -> check.body
        }
        write(KidNotice.Adjusted) { token -> client.adjust(url, token, kidId, body) }
    }

    fun confirmExcuse() {
        val d = _state.value.dialog as? KidDialog.Excuse ?: return
        write(KidNotice.Excused) { token -> client.excuse(url, token, d.instance.id, buildActionNote(d.note)) }
    }

    fun confirmReverse() {
        val d = _state.value.dialog as? KidDialog.Reverse ?: return
        write(KidNotice.Reversed) { token -> client.reverseApproval(url, token, d.instance.id, buildActionNote(d.note)) }
    }

    fun reassignTo(target: Kid) {
        val d = _state.value.dialog as? KidDialog.Reassign ?: return
        write(KidNotice.Reassigned) { token -> client.reassign(url, token, d.instance.id, target.id) }
    }

    /**
     * Runs one write. It closes the dialog and reloads on success and on a stale chore (the reload
     * shows what is true now), and keeps the dialog open on an offline or refused request so it can
     * be retried; a retry carries the same Idempotency-Key.
     */
    private fun write(done: KidNotice, call: suspend (token: String) -> ActionResult<*>) {
        if (_state.value.working) return
        val token = token() ?: return
        _state.update { it.copy(working = true) }
        viewModelScope.launch {
            val result = call(token)
            if (result == ActionResult.Unauthorized) tokens.clear()
            _state.update { s ->
                val left = s.copy(working = false)
                when (result) {
                    is ActionResult.Done -> left.copy(dialog = null, notice = done)
                    is ActionResult.Stale -> left.copy(dialog = null, notice = KidNotice.Stale(staleReasonFor(result.code)))
                    ActionResult.Unauthorized -> left.copy(signedOut = true)
                    ActionResult.Forbidden -> left.copy(notice = KidNotice.Failed("http_403"))
                    is ActionResult.Invalid -> left.copy(notice = KidNotice.Failed(result.code))
                    is ActionResult.Unreachable -> left.copy(notice = KidNotice.Offline(result.reason))
                    is ActionResult.Failed -> left.copy(notice = KidNotice.Failed(result.code))
                }
            }
            if (result is ActionResult.Done || result is ActionResult.Stale) refresh()
        }
    }

    fun noticeShown() = _state.update { it.copy(notice = null) }
}
