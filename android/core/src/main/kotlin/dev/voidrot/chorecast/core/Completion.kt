package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.CompleteChoreInstanceInputBody
import dev.voidrot.chorecast.api.models.EvidencePhotoInput
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneOffset
import java.util.UUID

/** The server's limits on one submission (`evidence.MaxPhotos`, `evidence.MaxNoteLength`). */
const val MAX_PHOTOS = 5
const val MAX_NOTE_LENGTH = 500

/**
 * One photo taken in the app. [capturedAt] and [captureId] are fixed at the shutter, and the file
 * is kept under [captureId] until the completion is accepted. [mediaId] is set once the upload
 * succeeded, so a retry doesn't upload it again.
 */
data class CapturedPhoto(val captureId: String, val capturedAt: Instant, val mediaId: Long? = null)

/** What the kid has put together so far for one instance. Survives a failed submit. */
data class CompletionDraft(
    val instanceId: Long,
    val ticked: Set<Int> = emptySet(),
    val note: String = "",
    val photos: List<CapturedPhoto> = emptyList(),
) {
    fun toggleStep(index: Int) = copy(ticked = if (index in ticked) ticked - index else ticked + index)

    fun withNote(text: String) = copy(note = text.take(MAX_NOTE_LENGTH))

    fun addPhoto(photo: CapturedPhoto) = if (photos.size >= MAX_PHOTOS) this else copy(photos = photos + photo)

    fun removePhoto(captureId: String) = copy(photos = photos.filterNot { it.captureId == captureId })

    /** Forgets the uploads the server no longer has, so the next submit uploads those photos again. */
    fun withoutMedia(captureIds: Set<String>) =
        copy(photos = photos.map { if (it.captureId in captureIds) it.copy(mediaId = null) else it })
}

fun newCaptureId(): String = UUID.randomUUID().toString()

/** What a chore asks for, as the complete flow needs it. */
data class CompletionNeeds(
    val steps: List<String>,
    val photos: Int,
    val beforeAfter: Boolean,
    val checklist: Boolean,
    val note: Boolean,
    val video: Boolean,
) {
    /** A before and an after photo need two photos, whatever the chore's count says. */
    val photosRequired: Int get() = if (beforeAfter) maxOf(photos, 2) else photos

    /** The role of the photo at [index]: the first is the before photo, the others come after it. */
    fun roleOf(index: Int): EvidencePhotoInput.Role? = when {
        !beforeAfter -> null
        index == 0 -> EvidencePhotoInput.Role.BEFORE
        else -> EvidencePhotoInput.Role.AFTER
    }

    /** What is still missing from [draft]. Empty means it can be submitted. */
    fun missing(draft: CompletionDraft): List<Missing> = buildList {
        if (video) add(Missing.Video)
        if (draft.photos.size < photosRequired) add(Missing.Photos(photosRequired - draft.photos.size))
        if (checklist && draft.ticked.count { it in steps.indices } < steps.size) add(Missing.Checklist)
        if (note && draft.note.isBlank()) add(Missing.Note)
    }

    sealed interface Missing {
        data class Photos(val more: Int) : Missing
        data object Checklist : Missing
        data object Note : Missing

        /** Video evidence isn't accepted yet (Phase 3), so such a chore can't be completed here. */
        data object Video : Missing
    }
}

/** The instance carries its chore's steps and evidence rules; absent rules ask for nothing. */
fun ChoreInstance.completionNeeds() = CompletionNeeds(
    steps = steps.orEmpty(),
    photos = evidence?.photos?.toInt() ?: 0,
    beforeAfter = evidence?.beforeAfter == true,
    checklist = evidence?.checklist == true,
    note = evidence?.note == true,
    video = evidence?.video == true,
)

/**
 * The completion request for [draft]. Every photo must have been uploaded. Ticked steps are sent
 * even when the chore doesn't require the checklist, and a blank note is left out.
 */
fun CompletionNeeds.request(draft: CompletionDraft): CompleteChoreInstanceInputBody =
    CompleteChoreInstanceInputBody(
        checklist = draft.ticked.filter { it in steps.indices }.sorted().map { it.toLong() },
        note = draft.note.trim().takeIf { it.isNotEmpty() },
        photos = draft.photos.mapIndexed { i, p ->
            EvidencePhotoInput(
                mediaId = checkNotNull(p.mediaId) { "photo ${p.captureId} is not uploaded" },
                captureId = p.captureId,
                capturedAt = OffsetDateTime.ofInstant(p.capturedAt, ZoneOffset.UTC),
                role = roleOf(i),
            )
        },
    )

/**
 * Idempotency keys for completion submits. One key per attempt: a retry of the same request
 * (same instance, same body) reuses it so the server replays the first answer instead of
 * completing twice; a changed body (another photo, a new upload id) gets a new key, because the
 * server refuses one key for two different bodies. Each instance keeps its own key, so another
 * chore's submit doesn't reset it. [clear] after a success, so a later resubmit after a
 * rejection is a new attempt.
 */
class IdempotencyKeys(private val generate: () -> String = { UUID.randomUUID().toString() }) {
    private val last = mutableMapOf<Long, Pair<CompleteChoreInstanceInputBody, String>>()

    fun keyFor(instanceId: Long, body: CompleteChoreInstanceInputBody): String {
        last[instanceId]?.takeIf { it.first == body }?.let { return it.second }
        return generate().also { last[instanceId] = body to it }
    }

    /** Forgets the key of [instanceId] once its completion was accepted. */
    fun clear(instanceId: Long) {
        last.remove(instanceId)
    }
}

/** Why a submit didn't complete the chore, as the screen needs to tell the kid. */
sealed interface CompleteError {
    /** Couldn't reach the server, or it answered 5xx. Safe to retry with the same draft. */
    data class Offline(val reason: String) : CompleteError

    /** The token was refused (session ended, device unpaired). */
    data object SignedOut : CompleteError

    /** 429 on upload: the kid holds the most pending photos, or two uploads are running. Try again later. */
    data object TooManyPhotos : CompleteError

    /** A photo is refused for good (duplicate of earlier evidence, too large, unsupported, file lost): retake it. */
    data class BadPhoto(val captureId: String, val reason: PhotoProblem) : CompleteError

    /**
     * 422: the server's evidence rules disagree with the draft. [photoIssues] are indexes of photos
     * at fault; [retake] are those whose capture itself is refused (capture time or id), which no
     * upload can fix. The others (the upload expired or was used) are uploaded again.
     */
    data class Invalid(
        val fields: List<String>,
        val photoIssues: Set<Int>,
        val retake: Set<Int>,
        val code: String,
    ) : CompleteError

    /** 422 video_evidence_unsupported. */
    data object VideoUnsupported : CompleteError

    /** 409 instance_closed: its day (or week) ended. A refresh shows it as missed. */
    data object Closed : CompleteError

    /** 409 instance_not_open / instance_planned: it already moved on; refresh. */
    data object NotOpen : CompleteError

    data class Failed(val code: String) : CompleteError
}

enum class PhotoProblem { DUPLICATE, TOO_LARGE, UNSUPPORTED, LOST }

/** True when trying again with the same draft can succeed without the kid changing anything. */
val CompleteError.retryable: Boolean
    get() = this is CompleteError.Offline || this is CompleteError.TooManyPhotos

/** Maps a failed completion (`POST /instances/{id}/completion`) to a [CompleteError]. */
fun completionError(status: Int, code: String, locations: List<String>): CompleteError = when {
    status == 401 -> CompleteError.SignedOut
    status >= 500 -> CompleteError.Offline("http_$status")
    code == "instance_closed" -> CompleteError.Closed
    code == "instance_not_open" || code == "instance_planned" -> CompleteError.NotOpen
    code == "video_evidence_unsupported" -> CompleteError.VideoUnsupported
    status == 422 -> CompleteError.Invalid(
        fields = locations.map { it.removePrefix("body.") },
        photoIssues = locations.mapNotNull { photoIndex(it) }.toSet(),
        retake = locations.filter { it.endsWith(".capturedAt") || it.endsWith(".captureId") }.mapNotNull { photoIndex(it) }.toSet(),
        code = code,
    )
    else -> CompleteError.Failed(code)
}

/** Maps a failed photo upload (`POST /media/photos`) to a [CompleteError]. */
fun uploadError(status: Int, code: String, captureId: String): CompleteError = when {
    status == 401 -> CompleteError.SignedOut
    status == 429 -> CompleteError.TooManyPhotos
    status >= 500 -> CompleteError.Offline("http_$status")
    status == 409 && code == "duplicate_media" -> CompleteError.BadPhoto(captureId, PhotoProblem.DUPLICATE)
    status == 413 -> CompleteError.BadPhoto(captureId, PhotoProblem.TOO_LARGE)
    status == 415 || status == 422 -> CompleteError.BadPhoto(captureId, PhotoProblem.UNSUPPORTED)
    else -> CompleteError.Failed(code)
}

private fun photoIndex(location: String): Int? = PHOTO_LOCATION.find(location)?.groupValues?.get(1)?.toInt()

private val PHOTO_LOCATION = Regex("""photos\[(\d+)]""")
