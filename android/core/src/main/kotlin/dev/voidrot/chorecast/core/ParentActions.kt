package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.ChoreInstanceNoteInputBody
import dev.voidrot.chorecast.api.models.Kid
import dev.voidrot.chorecast.api.models.PointsAdjustmentInput
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId

/** The server's limit on an adjustment's, excusal's and reversal's note, in code points. */
const val NOTE_MAX = 500

/** The server's limit on an adjustment's size. */
const val ADJUST_AMOUNT_MAX = 1_000_000L

/** How many days back a kid's day view reaches, so a recent miss can still be excused. */
const val KID_DAY_LOOKBACK = 7L

/**
 * The per-chore actions a parent has on a kid's day, besides approving and rejecting (the review
 * queue does those).
 */
enum class ParentAction { REVERSE, EXCUSE, REASSIGN }

/**
 * The actions the server accepts for [i] in its current state: reverse an approval, excuse a missed
 * chore, reassign an open one. A shared chore has no single assignee to move.
 */
fun actionsFor(i: ChoreInstance): List<ParentAction> = when (i.status) {
    ChoreInstance.Status.APPROVED -> listOf(ParentAction.REVERSE)
    ChoreInstance.Status.MISSED -> listOf(ParentAction.EXCUSE)
    ChoreInstance.Status.OPEN -> if (i.kidId != null) listOf(ParentAction.REASSIGN) else emptyList()
    else -> emptyList()
}

/** Kids a chore can move to: everyone but its current assignee. */
fun reassignTargets(kids: List<Kid>, i: ChoreInstance): List<Kid> = kids.filter { it.id != i.kidId }

enum class AdjustmentProblem { AMOUNT_INVALID, NOTE_REQUIRED, NOTE_TOO_LONG }

sealed interface AdjustmentCheck {
    data class Valid(val body: PointsAdjustmentInput) : AdjustmentCheck
    data class Invalid(val problem: AdjustmentProblem) : AdjustmentCheck
}

/**
 * Validates the adjustment form as the server will: a whole amount from 1 to [ADJUST_AMOUNT_MAX]
 * (the sign comes from [subtract]) and a note of 1 to [NOTE_MAX] code points once trimmed.
 */
fun buildAdjustment(subtract: Boolean, amountText: String, note: String): AdjustmentCheck {
    val amount = amountText.trim().takeIf { s -> s.isNotEmpty() && s.all { it in '0'..'9' } }?.toLongOrNull()
    if (amount == null || amount < 1 || amount > ADJUST_AMOUNT_MAX) {
        return AdjustmentCheck.Invalid(AdjustmentProblem.AMOUNT_INVALID)
    }
    val trimmed = note.trim()
    if (trimmed.isEmpty()) return AdjustmentCheck.Invalid(AdjustmentProblem.NOTE_REQUIRED)
    if (trimmed.codePointCount(0, trimmed.length) > NOTE_MAX) {
        return AdjustmentCheck.Invalid(AdjustmentProblem.NOTE_TOO_LONG)
    }
    return AdjustmentCheck.Valid(PointsAdjustmentInput(amount = if (subtract) -amount else amount, note = trimmed))
}

/**
 * The optional note on an excusal or a reversal. It is always sent as a JSON body (`{}` when empty),
 * because the server deduplicates only JSON POSTs and a body-less retry could meet a 409.
 */
fun buildActionNote(note: String): ChoreInstanceNoteInputBody =
    ChoreInstanceNoteInputBody(note = note.trim().takeCodePoints(NOTE_MAX).trim().ifEmpty { null })

/** Why a write found the screen's copy of a chore out of date. */
enum class StaleReason { NOT_FOUND, CLOSED, TAKEN, SHARED, CHANGED }

private val STALE_CODES = setOf(
    "instance_not_found",
    "instance_not_open",
    "instance_not_submitted",
    "instance_not_approved",
    "instance_not_missed",
    "instance_closed",
    "instance_planned",
    "instance_shared",
    "instance_taken",
)

fun staleReasonFor(code: String): StaleReason = when (code) {
    "instance_not_found" -> StaleReason.NOT_FOUND
    "instance_closed" -> StaleReason.CLOSED
    "instance_taken" -> StaleReason.TAKEN
    "instance_shared" -> StaleReason.SHARED
    else -> StaleReason.CHANGED
}

/** The outcome of one parent write; [T] is what the server returned. */
sealed interface ActionResult<out T> {
    data class Done<T>(val value: T) : ActionResult<T>

    /**
     * The chore is not in the state the action needs: someone else got there first, the day closed, or
     * a retry of a body-less request already took effect. Reload to see the truth.
     */
    data class Stale(val code: String) : ActionResult<Nothing>
    data object Unauthorized : ActionResult<Nothing>
    data object Forbidden : ActionResult<Nothing>

    /** The server refused the input; [code] is its error code. */
    data class Invalid(val code: String) : ActionResult<Nothing>
    data class Unreachable(val reason: String) : ActionResult<Nothing>
    data class Failed(val code: String) : ActionResult<Nothing>
}

/** Whether the server has answered for sure, so the attempt's Idempotency-Key can be dropped. */
val ActionResult<*>.outcomeKnown: Boolean
    get() = when (this) {
        is ActionResult.Unreachable -> false
        is ActionResult.Failed -> failureOutcomeKnown(code)
        else -> true
    }

/** Maps a failed parent write to an [ActionResult]. Only the chore-state codes count as stale. */
fun actionFailureFor(status: Int, code: String): ActionResult<Nothing> = when {
    status == 401 -> ActionResult.Unauthorized
    status == 403 -> ActionResult.Forbidden
    (status == 404 || status == 409) && code in STALE_CODES -> ActionResult.Stale(code)
    status == 422 -> ActionResult.Invalid(code)
    else -> ActionResult.Failed(code)
}

fun adjustAction(kidId: Long, body: PointsAdjustmentInput) = "adjust:$kidId:${body.amount}:${body.note}"

fun excuseAction(instanceId: Long, body: ChoreInstanceNoteInputBody) = "excuse:$instanceId:${body.note.orEmpty()}"

fun reverseAction(instanceId: Long, body: ChoreInstanceNoteInputBody) = "reverse:$instanceId:${body.note.orEmpty()}"

fun reassignAction(instanceId: Long, kidId: Long) = "reassign:$instanceId:$kidId"

/**
 * The household's current date: the day whose close is still ahead. [dayEnd] is "HH:MM"; "00:00" and
 * "24:00" mean midnight. An unreadable [dayEnd] is read as midnight.
 */
fun householdToday(zone: ZoneId, dayEnd: String, now: Instant): LocalDate {
    val local = now.atZone(zone)
    val wall = local.hour * 60 + local.minute
    return if (wall >= dayEndMinutes(dayEnd)) local.toLocalDate().plusDays(1) else local.toLocalDate()
}

private fun dayEndMinutes(dayEnd: String): Int {
    val parts = dayEnd.split(":")
    val h = parts.getOrNull(0)?.toIntOrNull()
    val m = parts.getOrNull(1)?.toIntOrNull()
    if (parts.size != 2 || h == null || m == null) return 24 * 60
    val minutes = h * 60 + m
    return if (minutes == 0) 24 * 60 else minutes
}

enum class DayBucket { ATTENTION, TODAY, EARLIER }

/** Missed and submitted chores need a parent; today's others come next; finished days last. */
fun bucketOf(i: ChoreInstance, today: LocalDate): DayBucket = when {
    i.status == ChoreInstance.Status.MISSED || i.status == ChoreInstance.Status.SUBMITTED -> DayBucket.ATTENTION
    i.status == ChoreInstance.Status.OPEN || i.date == today -> DayBucket.TODAY
    else -> DayBucket.EARLIER
}

/** A kid's recent chores in three groups, each oldest day first; forecasts are left out. */
data class KidDayPlan(
    val attention: List<ChoreInstance>,
    val today: List<ChoreInstance>,
    val earlier: List<ChoreInstance>,
) {
    val isEmpty: Boolean get() = attention.isEmpty() && today.isEmpty() && earlier.isEmpty()
}

fun planKidDay(kidId: Long, instances: List<ChoreInstance>, today: LocalDate): KidDayPlan {
    val mine = instances
        .filter { !it.planned && (it.kidId == kidId || (it.kidId == null && kidId in it.groupKidIds.orEmpty())) }
        .sortedWith(compareBy<ChoreInstance>({ it.date }, { it.slot.orEmpty() }, { it.id }))
    val by = mine.groupBy { bucketOf(it, today) }
    return KidDayPlan(
        attention = by[DayBucket.ATTENTION].orEmpty(),
        today = by[DayBucket.TODAY].orEmpty(),
        earlier = by[DayBucket.EARLIER].orEmpty(),
    )
}
