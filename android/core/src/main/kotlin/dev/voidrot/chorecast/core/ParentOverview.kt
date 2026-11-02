package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.DailyStreak
import dev.voidrot.chorecast.api.models.Kid
import dev.voidrot.chorecast.api.models.PointsBalance
import dev.voidrot.chorecast.api.models.ReviewQueueItem

/**
 * One kid's row on the parent home. [total] and [done] count today's chores, leaving out planned
 * days and excused chores; a chore the kid submitted counts as done (it waits for a parent).
 * [balance] and [streak] are null when theirs couldn't be read.
 */
data class KidOverview(
    val kid: Kid,
    val done: Int,
    val total: Int,
    val balance: PointsBalance?,
    val streak: DailyStreak?,
    val pendingReviews: Int,
)

/**
 * The parent home. [pendingMore] is true when the queue has more pages than were read, so the
 * counts are a lower bound ("12+").
 */
data class ParentOverview(val kids: List<KidOverview>, val pendingMore: Boolean) {
    val pendingTotal: Int get() = kids.sumOf { it.pendingReviews }
}

/** Today's chores for [kidId]: planned and excused ones are left out; a shared one counts for each member. */
fun todaysChoresFor(kidId: Long, instances: List<ChoreInstance>): List<ChoreInstance> = instances.filter {
    !it.planned && it.status != ChoreInstance.Status.EXCUSED &&
        (it.kidId == kidId || (it.kidId == null && kidId in it.groupKidIds.orEmpty()))
}

fun buildParentOverview(
    kids: List<Kid>,
    instances: List<ChoreInstance>,
    balances: Map<Long, PointsBalance>,
    streaks: Map<Long, DailyStreak>,
    queue: List<ReviewQueueItem>,
    queueHasMore: Boolean,
): ParentOverview {
    val pending = queue.groupingBy { it.kidIdOrNull() }.eachCount()
    return ParentOverview(
        kids = kids.sortedBy { it.displayName.lowercase() }.map { kid ->
            val today = todaysChoresFor(kid.id, instances)
            KidOverview(
                kid = kid,
                done = today.count {
                    it.status == ChoreInstance.Status.APPROVED || it.status == ChoreInstance.Status.SUBMITTED
                },
                total = today.size,
                balance = balances[kid.id],
                streak = streaks[kid.id],
                pendingReviews = pending[kid.id] ?: 0,
            )
        },
        pendingMore = queueHasMore,
    )
}
