package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.ChoreInstanceRejection
import dev.voidrot.chorecast.api.models.EvidenceRules
import kotlinx.serialization.decodeFromString
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.LocalDate
import java.time.LocalTime
import java.time.OffsetDateTime

class KidTodayTest {
    private val day = LocalDate.of(2026, 5, 4)
    private val due = OffsetDateTime.parse("2026-05-04T20:00:00-05:00")

    private fun instance(
        id: Long,
        name: String = "Chore $id",
        slot: String? = null,
        status: ChoreInstance.Status = ChoreInstance.Status.OPEN,
        planned: Boolean = false,
        date: LocalDate = day,
        rejected: Boolean = false,
        completedAt: OffsetDateTime? = null,
        choreId: Long = id,
        points: Long? = null,
        steps: List<String>? = null,
        evidence: EvidenceRules? = null,
    ) = ChoreInstance(
        choreId = choreId,
        choreName = name,
        date = date,
        dueAt = due,
        id = id,
        planned = planned,
        status = status,
        slot = slot,
        completedAt = completedAt,
        points = points,
        steps = steps,
        evidence = evidence,
        lastRejection = if (rejected) ChoreInstanceRejection(rejectedAt = due, text = "again") else null,
    )

    @Test
    fun groupsTodayBySlotWithDayCloseLast() {
        val plan = buildTodayPlan(
            listOf(
                instance(1, slot = null),
                instance(2, slot = "18:00"),
                instance(3, slot = "07:30", name = "b"),
                instance(4, slot = "07:30", name = "A"),
            ),
        )
        assertEquals(listOf(LocalTime.of(7, 30), LocalTime.of(18, 0), null), plan.today.map { it.time })
        // Same slot: by name, ignoring case.
        assertEquals(listOf(4L, 3L), plan.today[0].chores.map { it.instance.id })
    }

    @Test
    fun plannedInstancesGoToTheirDayNotToday() {
        val tomorrow = day.plusDays(1)
        val plan = buildTodayPlan(
            listOf(
                instance(1, planned = true, date = day.plusDays(2)),
                instance(2),
                instance(3, planned = true, date = tomorrow, slot = "09:00"),
                instance(4, planned = true, date = tomorrow, slot = "08:00"),
            ),
        )
        assertEquals(listOf(2L), plan.today.flatMap { it.chores }.map { it.instance.id })
        assertEquals(listOf(tomorrow, day.plusDays(2)), plan.planned.map { it.date })
        assertEquals(listOf(4L, 3L), plan.planned[0].chores.map { it.instance.id })
    }

    @Test
    fun pointsComeFromTheInstanceAndAreNullWhenAbsent() {
        val plan = buildTodayPlan(listOf(instance(1, points = 15), instance(2)))
        assertEquals(15L, plan.find(1)?.points)
        assertNull(plan.find(2)?.points)
        assertNull(plan.find(99))
    }

    @Test
    fun aRejectedOpenChoreShowsAsRedo() {
        assertEquals(ChoreStatusView.REDO, instance(1, rejected = true).statusView())
        assertEquals(ChoreStatusView.OPEN, instance(1).statusView())
        // A resubmission keeps lastRejection but is waiting for review again.
        assertEquals(ChoreStatusView.SUBMITTED, instance(1, status = ChoreInstance.Status.SUBMITTED, rejected = true).statusView())
    }

    @Test
    fun aRejectionOlderThanTheLastCompletionIsNotShown() {
        // Rejected, resubmitted and approved later, then the approval was reversed: open, not "do it again".
        val reversed = instance(1, rejected = true, completedAt = due.plusHours(2))
        assertEquals(ChoreStatusView.OPEN, reversed.statusView())
        assertFalse(reversed.isRejected())
        // Completed, then rejected: back to the kid with feedback.
        assertEquals(ChoreStatusView.REDO, instance(1, rejected = true, completedAt = due.minusHours(2)).statusView())
        assertTrue(instance(1, rejected = true, completedAt = null).isRejected())
        assertFalse(instance(1, completedAt = due).isRejected())
    }

    @Test
    fun everyStatusMaps() {
        val expected = mapOf(
            ChoreInstance.Status.OPEN to ChoreStatusView.OPEN,
            ChoreInstance.Status.SUBMITTED to ChoreStatusView.SUBMITTED,
            ChoreInstance.Status.APPROVED to ChoreStatusView.APPROVED,
            ChoreInstance.Status.MISSED to ChoreStatusView.MISSED,
            ChoreInstance.Status.EXCUSED to ChoreStatusView.EXCUSED,
        )
        for ((status, view) in expected) assertEquals(view, instance(1, status = status).statusView())
    }

    @Test
    fun aStatusFromANewerServerDecodesAndIsNotActionable() {
        val json = """
            {"choreId":1,"choreName":"Dishes","date":"2026-05-04","dueAt":"2026-05-04T20:00:00-05:00",
             "id":1,"planned":false,"status":"frozen"}
        """.trimIndent()
        val decoded = ApiClients.json.decodeFromString<ChoreInstance>(json)
        assertEquals(ChoreInstance.Status.UNKNOWN_DEFAULT_OPEN_API, decoded.status)
        assertEquals(ChoreStatusView.UNKNOWN, decoded.statusView())
        assertFalse(TodayChore(decoded).canComplete)
    }

    @Test
    fun onlyTodaysOpenChoresCanBeCompleted() {
        assertTrue(TodayChore(instance(1)).canComplete)
        assertTrue(TodayChore(instance(1, rejected = true)).canComplete)
        assertFalse(TodayChore(instance(1, planned = true)).canComplete)
        for (s in listOf(ChoreInstance.Status.SUBMITTED, ChoreInstance.Status.APPROVED, ChoreInstance.Status.MISSED, ChoreInstance.Status.EXCUSED)) {
            assertFalse(TodayChore(instance(1, status = s)).canComplete)
        }
    }

    @Test
    fun malformedSlotCountsAsDayClose() {
        assertNull(slotTime("soon"))
        assertNull(slotTime(null))
        assertEquals(LocalTime.of(9, 5), slotTime("09:05"))
    }

    @Test
    fun aTodayListGoesStaleAfterAMinute() {
        val loaded = due.toInstant()
        assertTrue(isTodayStale(null, loaded))
        assertFalse(isTodayStale(loaded, loaded.plusSeconds(30)))
        assertFalse(isTodayStale(loaded, loaded.plus(TODAY_STALE_AFTER)))
        assertTrue(isTodayStale(loaded, loaded.plus(TODAY_STALE_AFTER).plusSeconds(1)))
        // The clock moved back: the age can't be trusted.
        assertTrue(isTodayStale(loaded, loaded.minusSeconds(5)))
    }

    @Test
    fun emptyPlanIsEmpty() {
        assertTrue(buildTodayPlan(emptyList()).isEmpty)
        assertFalse(buildTodayPlan(listOf(instance(1, planned = true))).isEmpty)
    }

    @Test
    fun evidenceRequirementsListWhatIsAsked() {
        assertEquals(emptyList<EvidenceRequirement>(), EvidenceRules(photos = 0).requirements())
        assertEquals(
            listOf(
                EvidenceRequirement.Photos(2, beforeAfter = true),
                EvidenceRequirement.Checklist,
                EvidenceRequirement.Note,
            ),
            EvidenceRules(photos = 2, beforeAfter = true, checklist = true, note = true, video = false).requirements(),
        )
        assertEquals(listOf(EvidenceRequirement.Video), EvidenceRules(photos = 0, video = true).requirements())
    }

    @Test
    fun completionNeedsComeFromTheInstance() {
        val needs = instance(
            1,
            steps = listOf("a", "b"),
            evidence = EvidenceRules(photos = 1, beforeAfter = true, checklist = true),
        ).completionNeeds()
        assertEquals(listOf("a", "b"), needs.steps)
        assertEquals(2, needs.photosRequired)
        assertTrue(needs.checklist)
        assertFalse(needs.note)
        // No rules and no steps: nothing to hand in.
        val none = instance(2).completionNeeds()
        assertTrue(none.missing(CompletionDraft(2)).isEmpty())
    }
}
