package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.EvidencePhoto
import dev.voidrot.chorecast.api.models.InstanceEvidence
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.net.URI
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.temporal.ChronoUnit

class KidHistoryTest {
    private val today = LocalDate.of(2026, 5, 4)
    private val at = OffsetDateTime.parse("2026-05-04T09:00:00Z")

    private fun instance(
        id: Long,
        date: LocalDate = today,
        status: ChoreInstance.Status = ChoreInstance.Status.APPROVED,
        planned: Boolean = false,
        slot: String? = null,
        name: String = "Chore $id",
    ) = ChoreInstance(
        choreId = id, choreName = name, date = date, dueAt = at, id = id, planned = planned, status = status, slot = slot,
    )

    private fun submission(
        attempt: Long,
        checklist: List<Long> = emptyList(),
        note: String? = null,
        photos: List<EvidencePhoto> = emptyList(),
    ) = InstanceEvidence(attempt = attempt, checklist = checklist, photos = photos, submittedAt = at, note = note)

    @Test
    fun groupsFinishedChoresByDateNewestFirst() {
        val days = groupHistory(
            listOf(
                instance(1, date = today.minusDays(2)),
                instance(2, date = today),
                instance(3, date = today.minusDays(2), slot = "08:00", name = "b"),
                instance(4, date = today.minusDays(2), slot = "08:00", name = "A"),
            ),
        )
        assertEquals(listOf(today, today.minusDays(2)), days.map { it.date })
        // Within a day: by due time (day close last), then name ignoring case.
        assertEquals(listOf(4L, 3L, 1L), days[1].chores.map { it.id })
    }

    @Test
    fun historyLeavesOutOpenAndPlannedChores() {
        val days = groupHistory(
            listOf(
                instance(1, status = ChoreInstance.Status.OPEN),
                instance(2, status = ChoreInstance.Status.OPEN, planned = true),
                instance(3, status = ChoreInstance.Status.SUBMITTED),
                instance(4, status = ChoreInstance.Status.MISSED),
                instance(5, status = ChoreInstance.Status.EXCUSED),
                instance(6, status = ChoreInstance.Status.APPROVED),
            ),
        )
        assertEquals(listOf(3L, 4L, 5L, 6L), days.single().chores.map { it.id })
    }

    @Test
    fun statusMappingShowsWhatHappenedToEachChore() {
        val views = ChoreInstance.Status.entries.map { instance(1, status = it).statusView() }
        assertEquals(
            listOf(
                ChoreStatusView.OPEN, ChoreStatusView.SUBMITTED, ChoreStatusView.APPROVED,
                ChoreStatusView.MISSED, ChoreStatusView.EXCUSED,
            ),
            views,
        )
    }

    @Test
    fun rangesPageBackwardsWithoutGapOrOverlapWithinTheServerLimit() {
        val first = latestHistoryRange(today)
        // Reaches a day past the device date, which may trail the household's.
        assertEquals(today.plusDays(1), first.to)
        assertEquals(HISTORY_PAGE_DAYS - 1L, ChronoUnit.DAYS.between(first.from, first.to))
        val second = first.older()
        assertEquals(first.from.minusDays(1), second.to)
        assertEquals(HISTORY_PAGE_DAYS - 1L, ChronoUnit.DAYS.between(second.from, second.to))
    }

    @Test
    fun loadingOlderMergesPagesWithoutDuplicates() {
        val first = latestHistoryRange(today)
        val h = firstHistory(first, listOf(instance(1), instance(2, date = today.minusDays(3))))
        val older = h.withOlder(first.older(), listOf(instance(2, date = today.minusDays(3)), instance(3, date = today.minusDays(70))))
        assertEquals(first.older(), older.oldest)
        assertEquals(2, older.pages)
        assertEquals(listOf(1L, 2L, 3L), older.days.flatMap { it.chores }.map { it.id })
    }

    @Test
    fun olderPagesStopAtTheCap() {
        var h = firstHistory(latestHistoryRange(today), emptyList())
        repeat(HISTORY_MAX_PAGES - 1) {
            assertTrue(h.canLoadOlder)
            h = h.withOlder(h.oldest.older(), emptyList())
        }
        assertEquals(HISTORY_MAX_PAGES, h.pages)
        assertFalse(h.canLoadOlder)
    }

    @Test
    fun evidenceViewMarksTickedStepsAndKeepsTheNote() {
        val view = listOf(submission(1, checklist = listOf(1, 9), note = "  all tidy ")).latestView(listOf("a", "b", "c"))!!
        assertEquals(listOf(StepView("a", false), StepView("b", true), StepView("c", false)), view.steps)
        assertEquals("all tidy", view.note)
    }

    @Test
    fun evidenceViewUsesTheLatestAttempt() {
        val view = listOf(submission(2, note = "second"), submission(1, note = "first")).latestView(emptyList())!!
        assertEquals("second", view.note)
    }

    @Test
    fun deletedPhotosAreExpiredAndLinksAreKept() {
        val view = listOf(
            submission(
                1,
                photos = listOf(
                    EvidencePhoto(mediaId = 1, url = URI("https://x.example/1"), role = EvidencePhoto.Role.BEFORE),
                    EvidencePhoto(mediaId = 2, expired = true, role = EvidencePhoto.Role.AFTER),
                    // A link-less photo that isn't flagged is still unusable.
                    EvidencePhoto(mediaId = 3),
                ),
            ),
        ).latestView(emptyList())!!
        assertEquals(
            listOf(
                PhotoView.Available("https://x.example/1", EvidencePhoto.Role.BEFORE),
                PhotoView.Expired(EvidencePhoto.Role.AFTER),
                PhotoView.Expired(null),
            ),
            view.photos,
        )
    }

    @Test
    fun noSubmissionsMeansNoEvidenceAndAnEmptyOneIsEmpty() {
        assertNull(emptyList<InstanceEvidence>().latestView(listOf("a")))
        assertTrue(listOf(submission(1)).latestView(emptyList())!!.isEmpty)
    }
}
