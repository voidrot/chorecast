package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.Chore
import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.EvidenceRules
import java.time.Duration
import java.time.Instant
import java.time.LocalDate
import java.time.LocalTime
import java.time.format.DateTimeParseException

/**
 * How an instance is shown to a kid. The API's `status` has no "rejected": a rejection sends the
 * instance back to `open` with `lastRejection` set, which the kid should see as "do it again".
 */
enum class ChoreStatusView { OPEN, REDO, SUBMITTED, APPROVED, MISSED, EXCUSED }

/**
 * True when the latest rejection is why the chore is open again. `lastRejection` is kept after a
 * later approval is reversed, so a rejection older than the last completion doesn't count.
 */
fun ChoreInstance.isRejected(): Boolean {
    val rejection = lastRejection ?: return false
    val completed = completedAt ?: return true
    return rejection.rejectedAt.isAfter(completed)
}

fun ChoreInstance.statusView(): ChoreStatusView = when (status) {
    ChoreInstance.Status.OPEN -> if (isRejected()) ChoreStatusView.REDO else ChoreStatusView.OPEN
    ChoreInstance.Status.SUBMITTED -> ChoreStatusView.SUBMITTED
    ChoreInstance.Status.APPROVED -> ChoreStatusView.APPROVED
    ChoreInstance.Status.MISSED -> ChoreStatusView.MISSED
    ChoreInstance.Status.EXCUSED -> ChoreStatusView.EXCUSED
}

/** One row of the today list. [points] is null when the chore's details couldn't be read. */
data class TodayChore(val instance: ChoreInstance, val points: Long?) {
    val status: ChoreStatusView = instance.statusView()

    /** Only today's open chores can be completed; planned days are a forecast. */
    val canComplete: Boolean get() = !instance.planned && instance.status == ChoreInstance.Status.OPEN
}

/** Chores sharing one due time today. A null [time] means "due at day close". */
data class SlotSection(val time: LocalTime?, val chores: List<TodayChore>)

/** A later day's forecast. */
data class PlannedDay(val date: LocalDate, val chores: List<TodayChore>)

data class TodayPlan(val today: List<SlotSection>, val planned: List<PlannedDay>) {
    val isEmpty: Boolean get() = today.isEmpty() && planned.isEmpty()

    fun find(instanceId: Long): TodayChore? =
        (today.flatMap { it.chores } + planned.flatMap { it.chores }).firstOrNull { it.instance.id == instanceId }
}

/**
 * Splits [instances] into today (grouped by due time, earliest first, day-close chores last) and the
 * planned days after it (grouped by date). The server decides "planned" (dated after today in the
 * household's timezone), so the device's clock and timezone don't matter. [chores] supplies points.
 */
fun buildTodayPlan(instances: List<ChoreInstance>, chores: Map<Long, Chore>): TodayPlan {
    val (planned, today) = instances.partition { it.planned }
    val order = compareBy<TodayChore>(
        { slotTime(it.instance.slot) ?: LocalTime.MAX },
        { it.instance.choreName.lowercase() },
        { it.instance.id },
    )
    fun row(i: ChoreInstance) = TodayChore(i, chores[i.choreId]?.points)
    return TodayPlan(
        today = today.map(::row).groupBy { slotTime(it.instance.slot) }
            .map { (time, rows) -> SlotSection(time, rows.sortedWith(order)) }
            .sortedBy { it.time ?: LocalTime.MAX },
        planned = planned.map(::row).groupBy { it.instance.date }
            .map { (date, rows) -> PlannedDay(date, rows.sortedWith(order)) }
            .sortedBy { it.date },
    )
}

/** How long a loaded today list is trusted when the screen comes back to the foreground. */
val TODAY_STALE_AFTER: Duration = Duration.ofMinutes(1)

/**
 * Whether the list loaded at [loadedAt] (null: never) should be reloaded at [now]. A clock that went
 * backwards counts as stale, since the age can't be trusted.
 */
fun isTodayStale(loadedAt: Instant?, now: Instant): Boolean =
    loadedAt == null || now.isBefore(loadedAt) || Duration.between(loadedAt, now) > TODAY_STALE_AFTER

/** Parses a slot ("HH:MM"); null when absent or malformed, which sorts and shows as day close. */
fun slotTime(slot: String?): LocalTime? = try {
    slot?.let(LocalTime::parse)
} catch (_: DateTimeParseException) {
    null
}

/** What a chore asks a kid to hand in. */
sealed interface EvidenceRequirement {
    /** [beforeAfter]: the photos must include a before and an after. */
    data class Photos(val count: Int, val beforeAfter: Boolean) : EvidenceRequirement
    data object Checklist : EvidenceRequirement
    data object Note : EvidenceRequirement
    data object Video : EvidenceRequirement
}

fun EvidenceRules.requirements(): List<EvidenceRequirement> = buildList {
    if (photos > 0) add(EvidenceRequirement.Photos(photos.toInt(), beforeAfter == true))
    if (checklist == true) add(EvidenceRequirement.Checklist)
    if (note == true) add(EvidenceRequirement.Note)
    if (video == true) add(EvidenceRequirement.Video)
}
