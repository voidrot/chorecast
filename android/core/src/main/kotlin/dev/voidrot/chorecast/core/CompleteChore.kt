package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.InstancesApi
import dev.voidrot.chorecast.api.apis.MediaApi
import dev.voidrot.chorecast.api.infrastructure.HttpResponse
import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.Problem
import io.ktor.client.engine.HttpClientEngine
import io.ktor.client.request.forms.FormPart
import io.ktor.client.request.forms.InputProvider
import io.ktor.http.Headers
import io.ktor.http.HttpHeaders
import io.ktor.util.reflect.typeInfo
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import kotlinx.io.Buffer
import kotlinx.io.write
import java.io.IOException

/** Where the photo files live between the shutter and an accepted completion. */
interface CaptureStore {
    /** The JPEG bytes of the capture, or null if the file is gone. */
    fun read(captureId: String): ByteArray?
    fun delete(captureId: String)
}

/** What a submit left behind: the [draft] to keep (uploads recorded) and how it ended. */
data class SubmitOutcome(val draft: CompletionDraft, val result: Result) {
    sealed interface Result {
        data class Completed(val instance: ChoreInstance) : Result
        data class Failed(val error: CompleteError) : Result
    }
}

/**
 * Uploads a draft's photos and completes the chore. An upload is retried by sending the same file
 * again: the server returns the pending photo it already has, so a retry costs no extra pending
 * slot. Nothing here retries by itself; the screen offers a retry and calls [submit] again.
 */
class ChoreCompleter(
    private val captures: CaptureStore,
    private val keys: IdempotencyKeys = IdempotencyKeys(),
    private val engine: HttpClientEngine? = null,
    // Outlives any screen, so a discard still runs after the ViewModel that asked for it is cleared.
    private val scope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.IO),
) {
    private fun media(url: ServerUrl, token: String) =
        MediaApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    private fun instances(url: ServerUrl, token: String) =
        InstancesApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    suspend fun submit(url: ServerUrl, token: String, needs: CompletionNeeds, draft: CompletionDraft): SubmitOutcome {
        if (needs.missing(draft).isNotEmpty()) return failed(draft, CompleteError.Failed("incomplete"))

        var current = draft
        for (photo in draft.photos) {
            if (photo.mediaId != null) continue
            when (val up = upload(url, token, photo.captureId)) {
                is Uploaded -> current = current.copy(
                    photos = current.photos.map { if (it.captureId == photo.captureId) it.copy(mediaId = up.id) else it },
                )
                is UploadFailed -> return failed(current, up.error)
            }
        }

        val body = needs.request(current)
        val key = keys.keyFor(current.instanceId, body)
        return try {
            val resp = instances(url, token).completeChoreInstance(current.instanceId, key, body)
            if (resp.success) {
                keys.clear(current.instanceId)
                current.photos.forEach { captures.delete(it.captureId) }
                // The submission now owns the uploads, so none is pending and none may be discarded.
                val attached = current.withoutMedia(current.photos.map { it.captureId }.toSet())
                SubmitOutcome(attached, SubmitOutcome.Result.Completed(resp.body()))
            } else {
                val problem = resp.problem()
                val error = completionError(resp.status, problem.code, problem.errors.orEmpty().mapNotNull { it.location })
                failed(current.afterInvalid(error), error)
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            failed(current, CompleteError.Offline(e.message ?: e::class.simpleName.orEmpty()))
        } catch (e: Exception) {
            failed(current, CompleteError.Failed(e.message ?: e::class.simpleName.orEmpty()))
        }
    }

    /** Frees the pending upload of [captureId], if [draft] has one. Call it as the photo leaves the draft. */
    fun discardRemoved(url: ServerUrl, token: String, draft: CompletionDraft, captureId: String): Job =
        discard(url, token, listOfNotNull(draft.photos.firstOrNull { it.captureId == captureId }?.mediaId))

    /** Frees every pending upload of a [draft] that is being dropped. */
    fun discardDropped(url: ServerUrl, token: String, draft: CompletionDraft): Job =
        discard(url, token, draft.photos.mapNotNull { it.mediaId })

    /**
     * Best effort and in the background: the kid never waits for it or sees it fail. 404 (gone or not
     * ours) and 409 (already attached) mean there is nothing left to free. A network failure is tried
     * once more; anything else is left to the server's 24 h expiry.
     */
    private fun discard(url: ServerUrl, token: String, mediaIds: List<Long>): Job = scope.launch {
        mediaIds.forEach { discardOne(url, token, it) }
    }

    private suspend fun discardOne(url: ServerUrl, token: String, id: Long) {
        repeat(DISCARD_ATTEMPTS) {
            try {
                // Any answer ends it: 2xx, 404 and 409 mean nothing is left to free; other statuses won't change on a retry.
                media(url, token).discardPhoto(id)
                return
            } catch (e: CancellationException) {
                throw e
            } catch (_: IOException) {
                // network failure: try again
            } catch (_: Exception) {
                return
            }
        }
    }

    /**
     * A photo whose capture the server refuses is dropped, because sending it again would fail the
     * same way; dropping it also changes the body, so the next submit gets a new key. A photo whose
     * upload the server refuses (expired, already used) is uploaded again.
     */
    private fun CompletionDraft.afterInvalid(error: CompleteError): CompletionDraft {
        if (error !is CompleteError.Invalid) return this
        val ids = { indexes: Set<Int> -> indexes.mapNotNull { photos.getOrNull(it)?.captureId }.toSet() }
        val retake = ids(error.retake)
        retake.forEach(captures::delete)
        return withoutMedia(ids(error.photoIssues - error.retake)).let { d ->
            d.copy(photos = d.photos.filterNot { it.captureId in retake })
        }
    }

    private fun failed(draft: CompletionDraft, error: CompleteError) =
        SubmitOutcome(draft, SubmitOutcome.Result.Failed(error))

    private sealed interface Upload
    private data class Uploaded(val id: Long) : Upload
    private data class UploadFailed(val error: CompleteError) : Upload

    private suspend fun upload(url: ServerUrl, token: String, captureId: String): Upload {
        val bytes = captures.read(captureId) ?: return UploadFailed(CompleteError.BadPhoto(captureId, PhotoProblem.LOST))
        return try {
            val part = FormPart(
                "file",
                InputProvider(bytes.size.toLong()) { Buffer().apply { write(bytes) } },
                Headers.build {
                    append(HttpHeaders.ContentType, "image/jpeg")
                    append(HttpHeaders.ContentDisposition, "filename=\"$captureId.jpg\"")
                },
            )
            val resp = media(url, token).uploadPhoto(part)
            if (resp.success) Uploaded(resp.body().id) else UploadFailed(uploadError(resp.status, resp.problem().code, captureId))
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            UploadFailed(CompleteError.Offline(e.message ?: e::class.simpleName.orEmpty()))
        } catch (e: Exception) {
            UploadFailed(CompleteError.Failed(e.message ?: e::class.simpleName.orEmpty()))
        }
    }

    private suspend fun HttpResponse<*>.problem(): Problem = try {
        typedBody<Problem>(typeInfo<Problem>())
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        Problem(code = "http_$status")
    }
}

private const val DISCARD_ATTEMPTS = 2
