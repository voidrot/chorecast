package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.EvidencePhotoInput
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class CompletionTest {
    private val t0 = Instant.parse("2026-05-04T13:00:00Z")
    private fun photo(id: String, media: Long? = null) = CapturedPhoto(id, t0, media)
    private val steps = listOf("Straighten", "Fluff")
    private fun needs(
        photos: Int = 0,
        beforeAfter: Boolean = false,
        checklist: Boolean = false,
        note: Boolean = false,
        video: Boolean = false,
    ) = CompletionNeeds(steps, photos, beforeAfter, checklist, note, video)

    @Test
    fun nothingRequiredMeansNothingMissing() {
        assertTrue(needs().missing(CompletionDraft(1)).isEmpty())
    }

    @Test
    fun reportsEachMissingPiece() {
        val n = needs(photos = 2, checklist = true, note = true)
        val draft = CompletionDraft(1).addPhoto(photo("a")).toggleStep(0).withNote("  ")
        assertEquals(
            listOf(CompletionNeeds.Missing.Photos(1), CompletionNeeds.Missing.Checklist, CompletionNeeds.Missing.Note),
            n.missing(draft),
        )
        val done = draft.addPhoto(photo("b")).toggleStep(1).withNote("done")
        assertTrue(n.missing(done).isEmpty())
    }

    @Test
    fun beforeAfterNeedsTwoPhotosEvenWhenChoreAsksForOne() {
        val n = needs(photos = 1, beforeAfter = true)
        assertEquals(2, n.photosRequired)
        assertEquals(listOf(CompletionNeeds.Missing.Photos(1)), n.missing(CompletionDraft(1).addPhoto(photo("a"))))
    }

    @Test
    fun videoChoreCannotBeCompleted() {
        assertEquals(listOf(CompletionNeeds.Missing.Video), needs(video = true).missing(CompletionDraft(1)))
    }

    @Test
    fun draftCapsPhotosAndNote() {
        var d = CompletionDraft(1)
        repeat(MAX_PHOTOS + 2) { d = d.addPhoto(photo("p$it")) }
        assertEquals(MAX_PHOTOS, d.photos.size)
        assertEquals(MAX_NOTE_LENGTH, d.withNote("x".repeat(MAX_NOTE_LENGTH + 10)).note.length)
        assertEquals(setOf(0), CompletionDraft(1).toggleStep(0).toggleStep(1).toggleStep(1).ticked)
    }

    @Test
    fun requestCarriesChecklistNotePhotosAndRoles() {
        val draft = CompletionDraft(1, ticked = setOf(1, 0, 9), note = " all done ", photos = listOf(photo("c1", 11), photo("c2", 12)))
        val body = needs(photos = 2, beforeAfter = true).request(draft)
        assertEquals(listOf(0L, 1L), body.checklist)
        assertEquals("all done", body.note)
        val photos = body.photos!!
        assertEquals(listOf(11L, 12L), photos.map { it.mediaId })
        assertEquals(listOf(EvidencePhotoInput.Role.BEFORE, EvidencePhotoInput.Role.AFTER), photos.map { it.role })
        assertEquals("c1", photos[0].captureId)
        assertEquals(t0.toEpochMilli(), photos[0].capturedAt!!.toInstant().toEpochMilli())
    }

    @Test
    fun requestHasNoRoleWithoutBeforeAfterAndDropsBlankNote() {
        val body = needs(photos = 1).request(CompletionDraft(1, note = " ", photos = listOf(photo("c1", 5))))
        assertEquals(null, body.note)
        assertEquals(null, body.photos!!.single().role)
    }

    @Test(expected = IllegalStateException::class)
    fun requestRefusesAnUnuploadedPhoto() {
        needs(photos = 1).request(CompletionDraft(1, photos = listOf(photo("c1"))))
    }

    @Test
    fun idempotencyKeyIsReusedOnlyForTheSameRequest() {
        var n = 0
        val keys = IdempotencyKeys { "k${++n}" }
        val a = needs(photos = 1).request(CompletionDraft(1, photos = listOf(photo("c1", 5))))
        val b = needs(photos = 1).request(CompletionDraft(1, photos = listOf(photo("c1", 6))))
        val first = keys.keyFor(1, a)
        assertEquals(first, keys.keyFor(1, a))
        val second = keys.keyFor(1, b)
        assertNotEquals(first, second)
        assertEquals(second, keys.keyFor(1, b))
        assertNotEquals(second, keys.keyFor(2, b))
        val third = keys.keyFor(2, b)
        keys.clear()
        assertNotEquals(third, keys.keyFor(2, b))
    }

    @Test
    fun mapsCompletionErrors() {
        assertEquals(CompleteError.SignedOut, completionError(401, "unauthorized", emptyList()))
        assertEquals(CompleteError.Closed, completionError(409, "instance_closed", emptyList()))
        assertEquals(CompleteError.NotOpen, completionError(409, "instance_not_open", emptyList()))
        assertEquals(CompleteError.VideoUnsupported, completionError(422, "video_evidence_unsupported", emptyList()))
        assertEquals(CompleteError.Offline("http_503"), completionError(503, "media_busy", emptyList()))
        val invalid = completionError(422, "validation_failed", listOf("body.photos[1].mediaId", "body.note"))
        assertEquals(CompleteError.Invalid(listOf("photos[1].mediaId", "note"), setOf(1), "validation_failed"), invalid)
        assertEquals(CompleteError.Failed("forbidden"), completionError(403, "forbidden", emptyList()))
    }

    @Test
    fun mapsUploadErrorsAndRetryability() {
        assertEquals(CompleteError.TooManyPhotos, uploadError(429, "rate_limited", "c"))
        assertEquals(CompleteError.BadPhoto("c", PhotoProblem.DUPLICATE), uploadError(409, "duplicate_media", "c"))
        assertEquals(CompleteError.BadPhoto("c", PhotoProblem.TOO_LARGE), uploadError(413, "x", "c"))
        assertEquals(CompleteError.Offline("http_503"), uploadError(503, "media_busy", "c"))
        assertTrue(CompleteError.Offline("x").retryable)
        assertTrue(CompleteError.TooManyPhotos.retryable)
        assertFalse(CompleteError.Closed.retryable)
        assertFalse(CompleteError.BadPhoto("c", PhotoProblem.DUPLICATE).retryable)
    }
}
