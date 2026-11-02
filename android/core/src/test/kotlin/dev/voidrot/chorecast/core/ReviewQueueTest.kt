package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.EvidencePhoto
import dev.voidrot.chorecast.api.models.RejectChoreInstanceInputBody
import dev.voidrot.chorecast.api.models.ReviewQueueApprovalResult
import dev.voidrot.chorecast.api.models.ReviewQueueItem
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.net.URI
import java.time.Instant
import java.time.LocalDate
import java.time.OffsetDateTime

class ReviewQueueTest {
    private fun item(id: Long, kidName: String, completedBy: Long? = null, kidId: Long? = null) = ReviewQueueItem(
        instance = ChoreInstance(
            choreId = id, choreName = "Chore $id", date = LocalDate.of(2026, 5, 4),
            dueAt = OffsetDateTime.parse("2026-05-04T20:00:00Z"), id = id, planned = false,
            status = ChoreInstance.Status.SUBMITTED, completedBy = completedBy, kidId = kidId,
        ),
        kidName = kidName,
        steps = emptyList(),
    )

    @Test
    fun groupsByKidInOrderOfTheirOldestItem() {
        val groups = groupReviewByKid(
            listOf(item(1, "Ana", completedBy = 10), item(2, "Ben", kidId = 11), item(3, "Ana", kidId = 10)),
        )
        assertEquals(listOf("Ana", "Ben"), groups.map { it.kidName })
        assertEquals(listOf(1L, 3L), groups[0].items.map { it.instance.id })
    }

    @Test
    fun completerWinsOverAssigneeAndNamelessItemsGroupByName() {
        val groups = groupReviewByKid(
            listOf(item(1, "Ana", completedBy = 10, kidId = 12), item(2, "Ana", kidId = 10), item(3, "Cy"), item(4, "Cy")),
        )
        assertEquals(listOf(2, 2), groups.map { it.items.size })
        assertEquals("name:Cy", groups[1].key)
    }

    @Test
    fun mergePageSkipsInstancesAlreadyListed() {
        val merged = mergeReviewPage(listOf(item(1, "A"), item(2, "A")), listOf(item(2, "A"), item(3, "A")))
        assertEquals(listOf(1L, 2L, 3L), merged.map { it.instance.id })
    }

    @Test
    fun selectionToggleAndPrune() {
        val sel = toggleSelected(toggleSelected(emptySet(), 1), 2)
        assertEquals(setOf(1L, 2L), sel)
        assertEquals(setOf(2L), toggleSelected(sel, 1))
        assertEquals(setOf(2L), pruneSelection(sel, listOf(item(2, "A"), item(3, "A"))))
    }

    @Test
    fun bulkChunksDedupeAndCapAtTheServerLimit() {
        val ids = (1L..250L).toList() + listOf(1L, 2L)
        val chunks = bulkChunks(ids)
        assertEquals(listOf(100, 100, 50), chunks.map { it.size })
        assertEquals((1L..250L).toList(), chunks.flatten())
        assertTrue(bulkChunks(emptyList()).isEmpty())
    }

    @Test
    fun summarizesResultsIntoApprovedDecidedAndFailed() {
        val summary = summarizeApprovals(
            listOf(
                ReviewQueueApprovalResult(instanceId = 1, approved = true),
                ReviewQueueApprovalResult(instanceId = 2, approved = false, code = "already_approved"),
                ReviewQueueApprovalResult(instanceId = 3, approved = false, code = "instance_not_submitted"),
                ReviewQueueApprovalResult(instanceId = 4, approved = false, code = "instance_not_found"),
                ReviewQueueApprovalResult(instanceId = 5, approved = false, code = "boom", message = "bad"),
                ReviewQueueApprovalResult(instanceId = 6, approved = false),
            ),
        )
        assertEquals(listOf(1L), summary.approved)
        assertEquals(listOf(2L, 3L, 4L), summary.alreadyDecided)
        assertEquals(listOf(ApprovalFailure(5, "boom", "bad"), ApprovalFailure(6, "error", "")), summary.failed)
        assertEquals(setOf(1L, 2L, 3L, 4L), summary.removable)
    }

    @Test
    fun rejectionNeedsAReasonOrText() {
        assertNull(buildRejection(null, "   "))
        val reasonOnly = buildRejection(RejectChoreInstanceInputBody.Reason.NEEDS_REDO, " ")!!
        assertEquals(RejectChoreInstanceInputBody.Reason.NEEDS_REDO, reasonOnly.reason)
        assertNull(reasonOnly.text)
        val textOnly = buildRejection(null, "  Corners please ")!!
        assertNull(textOnly.reason)
        assertEquals("Corners please", textOnly.text)
        assertEquals(REJECT_TEXT_MAX, buildRejection(null, "x".repeat(900))!!.text!!.length)
    }

    @Test
    fun rejectionTextIsCappedInCodePointsNotUtf16Units() {
        val text = buildRejection(null, "😀".repeat(600))!!.text!!
        assertEquals(REJECT_TEXT_MAX, text.codePointCount(0, text.length))
        assertEquals("ab", "abc".takeCodePoints(2))
    }

    @Test
    fun photoLinkLapsesAtItsExpiry() {
        val photo = EvidencePhoto(mediaId = 1, urlExpiresAt = OffsetDateTime.parse("2026-05-04T10:00:00Z"))
        assertTrue(photo.linkLapsed(Instant.parse("2026-05-04T10:00:01Z")))
        assertFalse(photo.linkLapsed(Instant.parse("2026-05-04T09:59:00Z")))
        assertFalse(EvidencePhoto(mediaId = 2).linkLapsed(Instant.now()))
    }

    @Test
    fun queueGoesStaleBeforeSignedPhotoLinksLapse() {
        val t = Instant.parse("2026-05-04T10:00:00Z")
        assertTrue(isQueueStale(null, t))
        assertFalse(isQueueStale(t, t.plusSeconds(7 * 60)))
        assertTrue(isQueueStale(t, t.plusSeconds(9 * 60)))
        assertTrue(isQueueStale(t, t.minusSeconds(1)))
    }

    @Test
    fun expiredPhotosHaveNoLink() {
        fun photo(expired: Boolean?, url: String?, thumb: String? = null) = EvidencePhoto(
            mediaId = 1, expired = expired, url = url?.let(URI::create), thumbUrl = thumb?.let(URI::create),
        )
        assertEquals("https://x/full", photo(false, "https://x/full", "https://x/t").openableUrl())
        assertEquals("https://x/t", photo(null, null, "https://x/t").openableUrl())
        assertNull(photo(true, "https://x/full").openableUrl())
    }

    @Test
    fun keyIsReusedUntilTheOutcomeIsKnown() {
        var n = 0
        val keys = AttemptKeys { "k${++n}" }
        val first = keys.begin("approve:1")
        assertEquals(first, keys.begin("approve:1"))
        assertNotEquals(first, keys.begin("approve:2"))
        keys.finish("approve:1", outcomeKnown = false) // timeout: the server may have seen it
        assertEquals(first, keys.begin("approve:1"))
        keys.finish("approve:1", outcomeKnown = true)
        assertNotEquals(first, keys.begin("approve:1"))
    }

    @Test
    fun actionNamesFollowTheRequestBody() {
        assertEquals(approveAllAction(listOf(3, 1, 2)), approveAllAction(listOf(1, 2, 3)))
        assertNotEquals(approveAllAction(listOf(1, 2)), approveAllAction(listOf(1, 3)))
        val a = RejectChoreInstanceInputBody(reason = RejectChoreInstanceInputBody.Reason.NOT_DONE, text = "a")
        assertNotEquals(rejectAction(1, a), rejectAction(1, a.copy(text = "b")))
        assertNotEquals(rejectAction(1, a), rejectAction(2, a))
    }
}
