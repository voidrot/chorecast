package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.EvidencePhoto
import dev.voidrot.chorecast.api.models.InstanceEvidence
import java.time.LocalDate
import java.time.LocalTime

/** The most days one `GET /instances` request may span (`maxInstanceRangeDays` on the server). */
const val HISTORY_PAGE_DAYS = 62

/** How many pages "load older" may fetch (about two years), so an empty history can't page forever. */
const val HISTORY_MAX_PAGES = 12

/** An inclusive range of days. */
data class DateRange(val from: LocalDate, val to: LocalDate) {
    /** The page just before this one, with no gap and no overlap. */
    fun older(): DateRange = DateRange(from.minusDays(HISTORY_PAGE_DAYS.toLong()), from.minusDays(1))
}

/** The newest history page: the [HISTORY_PAGE_DAYS] days ending [today]. */
fun latestHistoryRange(today: LocalDate): DateRange = DateRange(today.minusDays(HISTORY_PAGE_DAYS - 1L), today)

/** Chores that have happened, as opposed to ones still to do (`open`, which includes sent back) or planned. */
fun ChoreInstance.isHistory(): Boolean = !planned && status != ChoreInstance.Status.OPEN

/** One day of history, newest day first in [KidHistory.days]. */
data class HistoryDay(val date: LocalDate, val chores: List<ChoreInstance>)

/**
 * The chores loaded so far, back to [oldest]. [pages] counts the pages fetched. Each page is merged by
 * instance id, so a repeated fetch can't show a chore twice.
 */
data class KidHistory(
    val instances: List<ChoreInstance> = emptyList(),
    val oldest: DateRange,
    val pages: Int = 1,
) {
    val canLoadOlder: Boolean get() = pages < HISTORY_MAX_PAGES

    val days: List<HistoryDay> by lazy { groupHistory(instances) }

    /** Adds the page before [oldest], which was fetched as [range]. */
    fun withOlder(range: DateRange, page: List<ChoreInstance>) =
        KidHistory(instances = (instances + page).distinctBy { it.id }, oldest = range, pages = pages + 1)
}

fun firstHistory(range: DateRange, page: List<ChoreInstance>) = KidHistory(page.distinctBy { it.id }, range)

/** Groups finished chores by date, newest day first; within a day by due time, then name. */
fun groupHistory(instances: List<ChoreInstance>): List<HistoryDay> =
    instances.filter { it.isHistory() }
        .groupBy { it.date }
        .map { (date, rows) ->
            HistoryDay(
                date,
                rows.sortedWith(
                    compareBy<ChoreInstance>({ slotTime(it.slot) ?: LocalTime.MAX }, { it.choreName.lowercase() }, { it.id }),
                ),
            )
        }
        .sortedByDescending { it.date }

/** One checklist step and whether the kid ticked it. */
data class StepView(val text: String, val done: Boolean)

/** A photo of a submission. Retention deletes the file, after which there is no link. */
sealed interface PhotoView {
    val role: EvidencePhoto.Role?

    data class Available(val url: String, override val role: EvidencePhoto.Role?) : PhotoView
    data class Expired(override val role: EvidencePhoto.Role?) : PhotoView
}

/** What the kid handed in for a chore: one submission, mapped for display. */
data class EvidenceView(val note: String?, val steps: List<StepView>, val photos: List<PhotoView>) {
    val isEmpty: Boolean get() = note == null && steps.none { it.done } && photos.isEmpty()
}

fun EvidencePhoto.view(): PhotoView {
    val link = url?.toString()
    return if (expired == true || link == null) PhotoView.Expired(role) else PhotoView.Available(link, role)
}

/**
 * The latest submission (a rejected chore may have several; the last is the one that counts), with
 * [steps] of the instance marked ticked. Indexes outside the steps are ignored. Null when nothing
 * was submitted.
 */
fun List<InstanceEvidence>.latestView(steps: List<String>): EvidenceView? {
    val last = maxByOrNull { it.attempt } ?: return null
    val ticked = last.checklist.map { it.toInt() }.toSet()
    return EvidenceView(
        note = last.note?.trim()?.takeIf { it.isNotEmpty() },
        steps = steps.mapIndexed { i, text -> StepView(text, i in ticked) },
        photos = last.photos.map { it.view() },
    )
}
