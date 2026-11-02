package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.DailyStreak
import dev.voidrot.chorecast.api.models.Kid
import dev.voidrot.chorecast.api.models.PointsBalance
import dev.voidrot.chorecast.api.models.ReviewQueueItem
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.LocalDate
import java.time.OffsetDateTime

private typealias S = ChoreInstance.Status

class ParentOverviewTest {
    private fun kid(id: Long, name: String) =
        Kid(color = "#fff", displayName = name, id = id, pinRequired = false, pinSet = false, slug = name.lowercase())

    private fun inst(
        id: Long,
        status: ChoreInstance.Status,
        kidId: Long? = null,
        group: List<Long>? = null,
        planned: Boolean = false,
        completedBy: Long? = null,
    ) = ChoreInstance(
        choreId = id, choreName = "C$id", date = LocalDate.of(2026, 5, 4),
        dueAt = OffsetDateTime.parse("2026-05-04T20:00:00Z"), id = id, planned = planned, status = status,
        kidId = kidId, groupKidIds = group, completedBy = completedBy,
    )

    private fun queued(i: ChoreInstance, name: String) = ReviewQueueItem(instance = i, kidName = name, steps = emptyList())

    @Test
    fun countsTodaysProgressPerKid() {
        val instances = listOf(
            inst(1, S.APPROVED, kidId = 1),
            inst(2, S.SUBMITTED, kidId = 1),
            inst(3, S.OPEN, kidId = 1),
            inst(4, S.MISSED, kidId = 1),
            inst(5, S.EXCUSED, kidId = 1), // excused: not counted
            inst(6, S.OPEN, kidId = 1, planned = true), // tomorrow: not counted
            inst(7, S.OPEN, group = listOf(1, 2)), // shared: counts for each member
            inst(8, S.APPROVED, kidId = 2),
        )
        val overview = buildParentOverview(listOf(kid(1, "Ana"), kid(2, "Ben")), instances, emptyMap(), emptyMap(), emptyList(), false)
        val (ana, ben) = overview.kids
        assertEquals(2 to 5, ana.done to ana.total)
        assertEquals(1 to 2, ben.done to ben.total)
    }

    @Test
    fun attachesBalanceStreakAndPendingReviewsAndSortsByName() {
        val queue = listOf(
            queued(inst(1, S.SUBMITTED, kidId = 2), "Ben"),
            queued(inst(2, S.SUBMITTED, kidId = 2), "Ben"),
            queued(inst(3, S.SUBMITTED, group = listOf(1, 2), completedBy = 1), "ana"),
        )
        val overview = buildParentOverview(
            listOf(kid(2, "Ben"), kid(1, "ana")), emptyList(),
            mapOf(1L to PointsBalance(available = 5, balance = 7)),
            mapOf(2L to DailyStreak(best = 9, current = 3)),
            queue, queueHasMore = true,
        )
        assertEquals(listOf("ana", "Ben"), overview.kids.map { it.kid.displayName })
        val (a, b) = overview.kids
        assertEquals(1, a.pendingReviews)
        assertEquals(2, b.pendingReviews)
        assertEquals(3, overview.pendingTotal)
        assertTrue(overview.pendingMore)
        assertEquals(7L, a.balance!!.balance)
        assertNull(a.streak)
        assertEquals(3L, b.streak!!.current)
        assertNull(b.balance)
    }
}
