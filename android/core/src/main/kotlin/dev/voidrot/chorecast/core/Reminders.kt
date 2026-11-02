package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.HouseholdSettings
import dev.voidrot.chorecast.api.models.Kid
import java.time.DateTimeException
import java.time.Duration
import java.time.Instant
import java.time.LocalDate
import java.time.LocalTime
import java.time.ZoneId
import java.time.format.DateTimeParseException

/** What a reminder is about. */
enum class ReminderKind { SLOT_DUE, DAY_CLOSE, MORNING }

/**
 * One local notification to schedule at [at]. [key] is stable for the same thing across syncs (the
 * instance, or the household day), so scheduling it again replaces it instead of adding a second one.
 * [dueAt] is the slot or day close the reminder warns about (null for the morning overview).
 * [choreCount] chores are open; [firstChoreName] is the earliest one.
 */
data class Reminder(
    val key: String,
    val kind: ReminderKind,
    val at: Instant,
    val dueAt: Instant?,
    val choreCount: Int,
    val firstChoreName: String,
)

/** A daily window in wall-clock time that may wrap past midnight (20:00 to 07:00). */
data class QuietWindow(val start: LocalTime, val end: LocalTime) {
    fun contains(t: LocalTime): Boolean =
        if (start < end) t >= start && t < end else t >= start || t < end

    /** [at] itself when it is outside the window, otherwise the next moment the window ends. */
    fun release(at: Instant, zone: ZoneId): Instant {
        val local = at.atZone(zone)
        if (!contains(local.toLocalTime())) return at
        val today = local.toLocalDate()
        val end = today.atTime(end).atZone(zone).toInstant()
        return if (end.isAfter(at)) end else today.plusDays(1).atTime(this.end).atZone(zone).toInstant()
    }
}

/**
 * Kids without quiet hours of their own get the documented kid default (docs/features/notifications.md).
 * The API leaves `quietHours` absent until a parent sets it and does not expose the default.
 */
val DEFAULT_KID_QUIET_HOURS = QuietWindow(LocalTime.of(20, 0), LocalTime.of(7, 0))

/**
 * What the reminder rules need from the household and the kid. [dayEnd] is the kid's effective day end
 * ("HH:MM", 00:00 = midnight): their own override when set, else the household's.
 */
data class ReminderSettings(val zone: ZoneId, val dayEnd: String, val quiet: QuietWindow)

/** Null when the household timezone is unreadable, since no time can then be placed. */
fun reminderSettings(household: HouseholdSettings, kid: Kid): ReminderSettings? {
    val zone = try {
        ZoneId.of(household.timezone)
    } catch (_: DateTimeException) {
        return null
    }
    val override = kid.dayEndOverride?.takeIf { parseClock(it) != null }
    val quiet = kid.quietHours?.let { q ->
        val start = parseClock(q.start)
        val end = parseClock(q.end)
        if (start != null && end != null && start != end) QuietWindow(start, end) else null
    }
    return ReminderSettings(zone, override ?: household.dayEnd, quiet ?: DEFAULT_KID_QUIET_HOURS)
}

private fun parseClock(s: String): LocalTime? = try {
    LocalTime.parse(s)
} catch (_: DateTimeParseException) {
    null
}

val SLOT_REMINDER_LEAD: Duration = Duration.ofMinutes(30)
val DAY_CLOSE_REMINDER_LEAD: Duration = Duration.ofHours(1)

/** Slots further out than this are left to a later sync, so one sync never schedules a week of work. */
val REMINDER_HORIZON: Duration = Duration.ofHours(48)

/**
 * The reminders to have scheduled at [now], given the kid's [instances]. Every sync replaces the whole
 * schedule with this list. Rules:
 *
 * - Only open, non-flexible instances count; submitted, approved, missed and excused ones are done.
 * - Slot: 30 minutes before each slotted instance is due. Instances without a slot are covered by the
 *   day-close reminder.
 * - Day close: 1 hour before the kid's day closes, for what is still open on the current household day.
 * - Morning overview: at the end of the quiet hours, for what is open on the household day starting
 *   then. Today's is skipped once it has passed, so it goes out at most once per day.
 *
 * A reminder time already past [now] is dropped (never sent late). A time inside the quiet hours is
 * held until they end; if that is no longer before the slot or day close it warns about, it is dropped.
 */
fun planReminders(instances: List<ChoreInstance>, settings: ReminderSettings, now: Instant): List<Reminder> {
    val open = instances.filter { it.status == ChoreInstance.Status.OPEN && it.flexible != true }
    val order = compareBy<ChoreInstance>({ it.dueAt.toInstant() }, { it.choreName.lowercase() }, { it.id })

    /** Holds [raw] out of the quiet hours; null when it is past or no longer before [deadline]. */
    fun place(raw: Instant, deadline: Instant): Instant? {
        if (!raw.isAfter(now)) return null
        val at = settings.quiet.release(raw, settings.zone)
        return at.takeIf { it.isBefore(deadline) }
    }

    val slots = open.filter { slotTime(it.slot) != null }
        .filter { it.dueAt.toInstant() <= now.plus(REMINDER_HORIZON) }
        .mapNotNull { i ->
            val due = i.dueAt.toInstant()
            place(due.minus(SLOT_REMINDER_LEAD), due)?.let {
                Reminder("slot:${i.id}", ReminderKind.SLOT_DUE, it, due, 1, i.choreName)
            }
        }

    val today = householdToday(settings.zone, settings.dayEnd, now)
    val closing = open.filter { !it.planned && it.date == today }.sortedWith(order)
    val close = dayClose(today, settings)
    val dayClose = closing.firstOrNull()?.let { first ->
        place(close.minus(DAY_CLOSE_REMINDER_LEAD), close)?.let {
            Reminder("close:$today", ReminderKind.DAY_CLOSE, it, close, closing.size, first.choreName)
        }
    }

    val mornings = (0L..1L).mapNotNull { offset ->
        val at = now.atZone(settings.zone).toLocalDate()
            .plusDays(offset).atTime(settings.quiet.end).atZone(settings.zone).toInstant()
        if (!at.isAfter(now)) return@mapNotNull null
        val day = householdToday(settings.zone, settings.dayEnd, at)
        val chores = open.filter { it.date == day }.sortedWith(order)
        chores.firstOrNull()?.let {
            Reminder("morning:$day", ReminderKind.MORNING, at, null, chores.size, it.choreName)
        }
    }.distinctBy { it.key }

    return (slots + listOfNotNull(dayClose) + mornings).sortedWith(compareBy({ it.at }, { it.key }))
}

/** The instant [date] closes for [settings]' day end; 00:00 is the following midnight. */
internal fun dayClose(date: LocalDate, settings: ReminderSettings): Instant =
    date.atStartOfDay().plusMinutes(dayEndMinutes(settings.dayEnd).toLong()).atZone(settings.zone).toInstant()
