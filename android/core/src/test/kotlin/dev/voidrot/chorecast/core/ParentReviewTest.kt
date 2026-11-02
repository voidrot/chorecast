package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.RejectChoreInstanceInputBody
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.engine.mock.toByteArray
import io.ktor.client.request.HttpRequestData
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

class ParentReviewTest {
    private val url = (ServerUrl.parse("https://chores.example.com") as Result.Ok).value
    private val json = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString())

    private fun MockRequestHandleScope.reply(status: HttpStatusCode, body: String) = respond(body, status, json)
    private fun problem(code: String) = """{"title":"x","status":409,"code":"$code"}"""

    private val instanceJson = """{"id":1,"choreId":7,"choreName":"Bed","date":"2026-05-04",
        "dueAt":"2026-05-04T20:00:00Z","status":"approved","planned":false}"""

    private val kidJson = """{"id":1,"slug":"ana","displayName":"Ana","color":"#fff","pinRequired":false,"pinSet":false}"""

    @Test
    fun approveSendsBearerAndAnIdempotencyKey() = runTest {
        val seen = mutableListOf<HttpRequestData>()
        val client = ParentReview(MockEngine { req -> seen += req; reply(HttpStatusCode.OK, instanceJson) })
        assertEquals(Decision.Done, client.approve(url, "tok", 1))
        val req = seen.single()
        assertEquals(HttpMethod.Post, req.method)
        assertEquals("/api/v1/instances/1/approval", req.url.encodedPath)
        assertEquals("Bearer tok", req.headers[HttpHeaders.Authorization])
        assertTrue(!req.headers["Idempotency-Key"].isNullOrBlank())
    }

    @Test
    fun retryAfterATimeoutReusesTheKeyAndANewDecisionGetsANewOne() = runTest {
        val keys = mutableListOf<String?>()
        var calls = 0
        val client = ParentReview(MockEngine { req ->
            keys += req.headers["Idempotency-Key"]
            if (++calls == 1) throw IOException("timeout")
            reply(HttpStatusCode.OK, instanceJson)
        })
        assertTrue(client.approve(url, "tok", 1) is Decision.Unreachable)
        assertEquals(Decision.Done, client.approve(url, "tok", 1))
        assertEquals(Decision.Done, client.approve(url, "tok", 1))
        assertEquals(keys[0], keys[1])
        assertNotEquals(keys[1], keys[2])
    }

    @Test
    fun serverErrorKeepsTheKeyButAnAnswerDropsIt() = runTest {
        val keys = mutableListOf<String?>()
        var status = HttpStatusCode.InternalServerError
        val client = ParentReview(MockEngine { req ->
            keys += req.headers["Idempotency-Key"]
            reply(status, problem("internal"))
        })
        assertEquals(Decision.Failed("http_500"), client.approve(url, "tok", 1))
        status = HttpStatusCode.Conflict
        assertEquals(Decision.AlreadyDecided, client.approve(url, "tok", 1))
        assertEquals(keys[0], keys[1])
        client.approve(url, "tok", 1)
        assertNotEquals(keys[1], keys[2])
    }

    @Test
    fun mapsErrorStatuses() {
        assertEquals(Decision.Unauthorized, decisionFor(401, "x"))
        assertEquals(Decision.Forbidden, decisionFor(403, "x"))
        assertEquals(Decision.AlreadyDecided, decisionFor(409, "instance_not_submitted"))
        assertEquals(Decision.AlreadyDecided, decisionFor(404, "instance_not_found"))
        assertEquals(Decision.Invalid("validation_failed"), decisionFor(422, "validation_failed"))
        assertEquals(Decision.Failed("http_503"), decisionFor(503, "x"))
        assertTrue(Decision.Done.outcomeKnown)
        assertTrue(!Decision.Failed("http_503").outcomeKnown)
        assertTrue(Decision.Failed("http_400").outcomeKnown)
        assertTrue(!Decision.Unreachable("x").outcomeKnown)
    }

    @Test
    fun rejectSendsReasonAndText() = runTest {
        var body = ""
        var path = ""
        val client = ParentReview(MockEngine { req ->
            path = req.url.encodedPath
            body = String(req.body.toByteArray())
            reply(HttpStatusCode.OK, instanceJson)
        })
        val rejection = buildRejection(RejectChoreInstanceInputBody.Reason.NEEDS_REDO, "Corners")!!
        assertEquals(Decision.Done, client.reject(url, "tok", 1, rejection))
        assertEquals("/api/v1/instances/1/rejection", path)
        assertTrue(body.contains("\"reason\":\"needs_redo\""))
        assertTrue(body.contains("\"text\":\"Corners\""))
    }

    @Test
    fun rejectOnAnAlreadyDecidedChoreIsAlreadyDecided() = runTest {
        val client = ParentReview(MockEngine { reply(HttpStatusCode.Conflict, problem("instance_not_submitted")) })
        val body = buildRejection(null, "no")!!
        assertEquals(Decision.AlreadyDecided, client.reject(url, "tok", 1, body))
    }

    @Test
    fun bulkApprovalSplitsIntoChunksWithTheirOwnKeys() = runTest {
        val bodies = mutableListOf<String>()
        val keys = mutableListOf<String?>()
        val client = ParentReview(MockEngine { req ->
            assertEquals("/api/v1/review-queue/approvals", req.url.encodedPath)
            keys += req.headers["Idempotency-Key"]
            val text = String(req.body.toByteArray())
            bodies += text
            val ids = Regex("\\d+").findAll(text.substringAfter("[")).map { it.value }.toList()
            val results = ids.joinToString(",") {
                if (it == "3") """{"instanceId":3,"approved":false,"code":"already_approved"}"""
                else """{"instanceId":$it,"approved":true}"""
            }
            reply(HttpStatusCode.OK, """{"results":[$results]}""")
        })
        val out = client.approveAll(url, "tok", (1L..150L).toList() + 1L)
        assertEquals(2, bodies.size)
        assertEquals(2, keys.toSet().size)
        assertNull(out.stopped)
        assertEquals(149, out.summary.approved.size)
        assertEquals(listOf(3L), out.summary.alreadyDecided)
        assertTrue(out.unsent.isEmpty())
    }

    @Test
    fun bulkApprovalStopsAtTheFirstChunkThatIsNotAnswered() = runTest {
        var calls = 0
        val client = ParentReview(MockEngine {
            if (++calls == 1) reply(HttpStatusCode.OK, """{"results":[{"instanceId":1,"approved":true}]}""")
            else throw IOException("offline")
        })
        val out = client.approveAll(url, "tok", (1L..101L).toList())
        assertEquals(2, calls)
        assertTrue(out.stopped is Decision.Unreachable)
        assertEquals(listOf(1L), out.summary.approved)
        assertEquals(listOf(101L), out.unsent)
    }

    @Test
    fun overviewCombinesKidsProgressBalanceStreakAndQueue() = runTest {
        val client = ParentReview(MockEngine { req ->
            assertEquals("Bearer tok", req.headers[HttpHeaders.Authorization])
            when (req.url.encodedPath) {
                "/api/v1/kids" -> reply(HttpStatusCode.OK, """{"items":[$kidJson]}""")
                "/api/v1/instances" -> reply(
                    HttpStatusCode.OK,
                    """{"items":[
                    {"id":1,"choreId":7,"choreName":"Bed","date":"2026-05-04","dueAt":"2026-05-04T20:00:00Z","status":"submitted","planned":false,"kidId":1},
                    {"id":2,"choreId":8,"choreName":"Dish","date":"2026-05-04","dueAt":"2026-05-04T20:00:00Z","status":"open","planned":false,"kidId":1}]}""",
                )
                "/api/v1/kids/1/points" -> reply(HttpStatusCode.OK, """{"balance":12,"available":10}""")
                "/api/v1/kids/1/streaks" -> reply(HttpStatusCode.OK, """{"daily":{"current":4,"best":6},"chores":[]}""")
                "/api/v1/review-queue" -> reply(
                    HttpStatusCode.OK,
                    """{"items":[{"kidName":"Ana","steps":[],"instance":{"id":1,"choreId":7,"choreName":"Bed","date":"2026-05-04","dueAt":"2026-05-04T20:00:00Z","status":"submitted","planned":false,"kidId":1}}],"nextCursor":"c2"}""",
                )
                else -> error("unexpected ${req.url}")
            }
        })
        val overview = (client.overview(url, "tok") as Fetch.Ok).value
        val ana = overview.kids.single()
        assertEquals(1 to 2, ana.done to ana.total)
        assertEquals(12L, ana.balance!!.balance)
        assertEquals(4L, ana.streak!!.current)
        assertEquals(1, ana.pendingReviews)
        assertTrue(overview.pendingMore)
    }

    @Test
    fun overviewStillShowsKidsWhenTheSideRequestsFail() = runTest {
        val client = ParentReview(MockEngine { req ->
            if (req.url.encodedPath == "/api/v1/kids") {
                reply(HttpStatusCode.OK, """{"items":[$kidJson]}""")
            } else {
                reply(HttpStatusCode.InternalServerError, problem("internal"))
            }
        })
        val ana = (client.overview(url, "tok") as Fetch.Ok).value.kids.single()
        assertNull(ana.balance)
        assertEquals(0, ana.total)
    }

    @Test
    fun readErrorsMapToFetchResults() = runTest {
        assertEquals(Fetch.Unauthorized, ParentReview(MockEngine { reply(HttpStatusCode.Unauthorized, problem("unauthorized")) }).queue(url, "t"))
        assertEquals(Fetch.Failed("http_403"), ParentReview(MockEngine { reply(HttpStatusCode.Forbidden, problem("forbidden")) }).overview(url, "t"))
        assertTrue(ParentReview(MockEngine { throw IOException("down") }).queue(url, "t") is Fetch.Unreachable)
    }

    @Test
    fun queuePageCarriesTheCursor() = runTest {
        var query = ""
        val client = ParentReview(MockEngine { req ->
            query = req.url.encodedQuery
            reply(HttpStatusCode.OK, """{"items":[],"nextCursor":"abc"}""")
        })
        val page = (client.queue(url, "tok", "c1") as Fetch.Ok).value
        assertEquals("abc", page.nextCursor)
        assertTrue(query.contains("cursor=c1"))
    }
}
