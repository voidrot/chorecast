package dev.voidrot.chorecast.core

import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.content.TextContent
import io.ktor.http.headersOf
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException
import java.time.Instant

class CompleteChoreTest {
    private val url = (ServerUrl.parse("https://chores.example.com") as Result.Ok).value
    private val json = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString())
    private val problem = headersOf(HttpHeaders.ContentType, "application/problem+json")

    private val media = """{"id":41,"kind":"photo","mime":"image/webp","bytes":1,"width":1,"height":1,"url":"https://x/u",
        "thumbnail":{"width":1,"height":1,"url":"https://x/t"},"urlExpiresAt":"2026-05-05T10:00:00Z","createdAt":"2026-05-04T13:00:00Z"}"""
    private val instance = """{"id":1,"choreId":7,"choreName":"Make bed","date":"2026-05-04","dueAt":"2026-05-04T08:00:00-05:00",
        "slot":"08:00","status":"submitted","planned":false}"""

    private class Files(vararg ids: String) : CaptureStore {
        val files = ids.associateWith { byteArrayOf(1, 2, 3) }
        val deleted = mutableListOf<String>()
        override fun read(captureId: String) = files[captureId]
        override fun delete(captureId: String) {
            deleted += captureId
        }
    }

    private val needs = CompletionNeeds(listOf("a"), 1, false, false, false, false)
    private fun draft() = CompletionDraft(1, photos = listOf(CapturedPhoto("c1", Instant.parse("2026-05-04T13:00:00Z"))))

    @Test
    fun uploadsThenCompletesWithKeyAndEvidence() = runTest {
        val seen = mutableListOf<String>()
        var key: String? = null
        var body = ""
        val engine = MockEngine { req ->
            seen += "${req.method.value} ${req.url.encodedPath}"
            assertEquals("Bearer tok", req.headers[HttpHeaders.Authorization])
            if (req.url.encodedPath.endsWith("/media/photos")) {
                respond(media, HttpStatusCode.Created, json)
            } else {
                key = req.headers["Idempotency-Key"]
                body = (req.body as TextContent).text
                respond(instance, HttpStatusCode.OK, json)
            }
        }
        val files = Files("c1")
        val out = ChoreCompleter(files, engine = engine).submit(url, "tok", needs, draft())
        assertEquals(listOf("POST /api/v1/media/photos", "POST /api/v1/instances/1/completion"), seen)
        assertTrue(!key.isNullOrBlank())
        assertTrue(body.contains("\"mediaId\":41") && body.contains("\"captureId\":\"c1\"") && body.contains("capturedAt"))
        assertTrue(out.result is SubmitOutcome.Result.Completed)
        assertEquals(listOf("c1"), files.deleted)
    }

    @Test
    fun retryAfterOfflineReusesUploadAndKey() = runTest {
        val keys = mutableListOf<String?>()
        var uploads = 0
        var failNext = true
        val engine = MockEngine { req ->
            if (req.url.encodedPath.endsWith("/media/photos")) {
                uploads++
                respond(media, HttpStatusCode.Created, json)
            } else {
                keys += req.headers["Idempotency-Key"]
                if (failNext) {
                    failNext = false
                    throw IOException("no network")
                }
                respond(instance, HttpStatusCode.OK, json)
            }
        }
        val completer = ChoreCompleter(Files("c1"), engine = engine)
        val first = completer.submit(url, "tok", needs, draft())
        assertEquals(SubmitOutcome.Result.Failed(CompleteError.Offline("no network")), first.result)
        assertEquals(41L, first.draft.photos.single().mediaId)
        val second = completer.submit(url, "tok", needs, first.draft)
        assertTrue(second.result is SubmitOutcome.Result.Completed)
        assertEquals(1, uploads)
        assertEquals(2, keys.size)
        assertEquals(keys[0], keys[1])
    }

    @Test
    fun uploadCapMapsToTooManyPhotosAndSkipsCompletion() = runTest {
        var completions = 0
        val engine = MockEngine { req ->
            if (req.url.encodedPath.endsWith("/media/photos")) {
                respond("""{"code":"rate_limited"}""", HttpStatusCode.TooManyRequests, problem)
            } else {
                completions++
                respond(instance, HttpStatusCode.OK, json)
            }
        }
        val out = ChoreCompleter(Files("c1"), engine = engine).submit(url, "tok", needs, draft())
        assertEquals(SubmitOutcome.Result.Failed(CompleteError.TooManyPhotos), out.result)
        assertEquals(0, completions)
        assertEquals(null, out.draft.photos.single().mediaId)
    }

    @Test
    fun invalidPhotoIsUploadedAgainNextTime() = runTest {
        val engine = MockEngine { req ->
            if (req.url.encodedPath.endsWith("/media/photos")) {
                respond(media, HttpStatusCode.Created, json)
            } else {
                respond(
                    """{"code":"validation_failed","errors":[{"location":"body.photos[0].mediaId","message":"expired"}]}""",
                    HttpStatusCode.UnprocessableEntity,
                    problem,
                )
            }
        }
        val files = Files("c1")
        val out = ChoreCompleter(files, engine = engine).submit(url, "tok", needs, draft())
        val err = (out.result as SubmitOutcome.Result.Failed).error as CompleteError.Invalid
        assertEquals(setOf(0), err.photoIssues)
        assertEquals(null, out.draft.photos.single().mediaId)
        assertTrue(files.deleted.isEmpty())
    }

    @Test
    fun photoWithRefusedCaptureTimeIsDroppedSoTheKeyChanges() = runTest {
        val keys = mutableListOf<String?>()
        val engine = MockEngine { req ->
            if (req.method.value == "DELETE") {
                respond("", HttpStatusCode.NoContent)
            } else if (req.url.encodedPath.endsWith("/media/photos")) {
                respond(media, HttpStatusCode.Created, json)
            } else {
                keys += req.headers["Idempotency-Key"]
                if (keys.size == 1) {
                    respond(
                        """{"code":"validation_failed","errors":[{"location":"body.photos[0].capturedAt","message":"too early"}]}""",
                        HttpStatusCode.UnprocessableEntity,
                        problem,
                    )
                } else {
                    respond(instance, HttpStatusCode.OK, json)
                }
            }
        }
        val files = Files("c1", "c2")
        val completer = ChoreCompleter(files, engine = engine)
        val first = completer.submit(url, "tok", needs, draft())
        val err = (first.result as SubmitOutcome.Result.Failed).error as CompleteError.Invalid
        assertEquals(setOf(0), err.retake)
        assertTrue(first.draft.photos.isEmpty())
        assertEquals(listOf("c1"), files.deleted)
        // Resending the same draft is blocked until a new photo is taken ...
        assertTrue(completer.submit(url, "tok", needs, first.draft).result is SubmitOutcome.Result.Failed)
        assertEquals(1, keys.size)
        // ... and the new photo makes a new body, so a new key.
        val retaken = first.draft.addPhoto(CapturedPhoto("c2", Instant.parse("2026-05-04T14:00:00Z")))
        assertTrue(completer.submit(url, "tok", needs, retaken).result is SubmitOutcome.Result.Completed)
        assertEquals(2, keys.size)
        assertNotEquals(keys[0], keys[1])
    }

    @Test
    fun closedInstanceMapsAndKeepsFiles() = runTest {
        val engine = MockEngine { req ->
            if (req.url.encodedPath.endsWith("/media/photos")) {
                respond(media, HttpStatusCode.Created, json)
            } else {
                respond("""{"code":"instance_closed"}""", HttpStatusCode.Conflict, problem)
            }
        }
        val files = Files("c1")
        val out = ChoreCompleter(files, engine = engine).submit(url, "tok", needs, draft())
        assertEquals(SubmitOutcome.Result.Failed(CompleteError.Closed), out.result)
        assertTrue(files.deleted.isEmpty())
    }

    @Test
    fun lostFileAsksForRetake() = runTest {
        val engine = MockEngine { error("no request expected") }
        val out = ChoreCompleter(Files(), engine = engine).submit(url, "tok", needs, draft())
        assertEquals(SubmitOutcome.Result.Failed(CompleteError.BadPhoto("c1", PhotoProblem.LOST)), out.result)
    }

    @Test
    fun incompleteDraftIsNotSent() = runTest {
        val engine = MockEngine { error("no request expected") }
        val out = ChoreCompleter(Files(), engine = engine).submit(url, "tok", needs, CompletionDraft(1))
        assertTrue(out.result is SubmitOutcome.Result.Failed)
    }

    private fun uploaded(vararg ids: Pair<String, Long?>) = CompletionDraft(
        1,
        photos = ids.map { (c, m) -> CapturedPhoto(c, Instant.parse("2026-05-04T13:00:00Z"), m) },
    )

    private fun TestScope.discarding(status: HttpStatusCode, calls: MutableList<String>) =
        ChoreCompleter(
            Files(),
            engine = MockEngine { req ->
                calls += "${req.method.value} ${req.url.encodedPath}"
                respond("", status)
            },
            scope = backgroundScope,
        )

    @Test
    fun removingAnUploadedPhotoDiscardsExactlyThatId() = runTest {
        val calls = mutableListOf<String>()
        discarding(HttpStatusCode.NoContent, calls)
            .discardRemoved(url, "tok", uploaded("c1" to 41L, "c2" to 42L), "c2").join()
        assertEquals(listOf("DELETE /api/v1/media/photos/42"), calls)
    }

    @Test
    fun removingAPhotoThatWasNeverUploadedMakesNoCall() = runTest {
        val calls = mutableListOf<String>()
        discarding(HttpStatusCode.NoContent, calls).discardRemoved(url, "tok", uploaded("c1" to null), "c1").join()
        assertTrue(calls.isEmpty())
    }

    @Test
    fun droppingADraftDiscardsAllItsUploadedIds() = runTest {
        val calls = mutableListOf<String>()
        discarding(HttpStatusCode.NoContent, calls)
            .discardDropped(url, "tok", uploaded("c1" to 41L, "c2" to null, "c3" to 43L)).join()
        assertEquals(listOf("DELETE /api/v1/media/photos/41", "DELETE /api/v1/media/photos/43"), calls)
    }

    @Test
    fun aSubmittedDraftDiscardsNothing() = runTest {
        val engine = MockEngine { req ->
            if (req.url.encodedPath.endsWith("/completion")) respond(instance, HttpStatusCode.OK, json) else respond(media, HttpStatusCode.Created, json)
        }
        val completer = ChoreCompleter(Files("c1"), engine = engine, scope = backgroundScope)
        val out = completer.submit(url, "tok", needs, draft())
        assertTrue(out.result is SubmitOutcome.Result.Completed)
        val calls = mutableListOf<String>()
        discarding(HttpStatusCode.NoContent, calls).discardDropped(url, "tok", out.draft).join()
        assertTrue(calls.isEmpty())
    }

    @Test
    fun notFoundAndConflictCountAsDoneWithoutRetry() = runTest {
        for (status in listOf(HttpStatusCode.NotFound, HttpStatusCode.Conflict)) {
            val calls = mutableListOf<String>()
            discarding(status, calls).discardDropped(url, "tok", uploaded("c1" to 41L)).join()
            assertEquals(1, calls.size)
        }
    }

    @Test
    fun aNetworkFailureIsRetriedOnceAndNeverThrows() = runTest {
        var calls = 0
        val engine = MockEngine {
            calls++
            throw IOException("offline")
        }
        ChoreCompleter(Files(), engine = engine, scope = backgroundScope)
            .discardDropped(url, "tok", uploaded("c1" to 41L)).join()
        assertEquals(2, calls)
    }

    @Test
    fun serverErrorMakesOneCallAndEndsQuietly() = runTest {
        val calls = mutableListOf<String>()
        discarding(HttpStatusCode.InternalServerError, calls).discardDropped(url, "tok", uploaded("c1" to 41L)).join()
        assertEquals(1, calls.size)
    }

    @Test
    fun photosTheServerRefusesAreDiscardedToo() = runTest {
        val deletes = mutableListOf<String>()
        val deleted = CompletableDeferred<Unit>()
        val engine = MockEngine { req ->
            val path = req.url.encodedPath
            when {
                req.method.value == "DELETE" -> {
                    deletes += path
                    deleted.complete(Unit)
                    respond("", HttpStatusCode.NoContent)
                }
                path.endsWith("/media/photos") -> respond(media, HttpStatusCode.Created, json)
                else -> respond(
                    """{"code":"validation_failed","errors":[{"location":"body.photos[0].capturedAt","message":"too early"}]}""",
                    HttpStatusCode.UnprocessableEntity,
                    problem,
                )
            }
        }
        val out = ChoreCompleter(Files("c1"), engine = engine, scope = backgroundScope).submit(url, "tok", needs, draft())
        assertTrue(out.draft.photos.isEmpty())
        // The engine answers on its own threads, so wait in real time.
        withContext(Dispatchers.Default) { withTimeout(5_000) { deleted.await() } }
        assertEquals(listOf("/api/v1/media/photos/41"), deletes)
    }
}
