package dev.voidrot.chorecast.core

import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.MockRequestHandleScope
import io.ktor.client.engine.mock.respond
import io.ktor.client.engine.mock.respondError
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

class KidChoresTest {
    private val url = (ServerUrl.parse("https://chores.example.com") as Result.Ok).value
    private val json = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString())

    private val chore = """{"id":7,"name":"Make bed","points":10,"requiresApproval":true,
        "createdAt":"2026-05-01T10:00:00Z","updatedAt":"2026-05-01T10:00:00Z",
        "steps":["Straighten sheet","Fluff pillow"],"evidence":{"photos":1,"checklist":true}}"""

    private val instances = """{"items":[
        {"id":1,"choreId":7,"choreName":"Make bed","date":"2026-05-04","dueAt":"2026-05-04T08:00:00-05:00","slot":"08:00",
         "status":"open","planned":false,"rejectionCount":1,
         "lastRejection":{"rejectedAt":"2026-05-03T20:00:00Z","reason":"needs_redo","text":"Corners please"}},
        {"id":2,"choreId":7,"choreName":"Make bed","date":"2026-05-05","dueAt":"2026-05-05T08:00:00-05:00","slot":"08:00",
         "status":"open","planned":true}]}"""

    private fun MockRequestHandleScope.ok(body: String) = respond(body, HttpStatusCode.OK, json)

    @Test
    fun loadsTodayWithPointsBalanceAndStreak() = runTest {
        val seen = mutableListOf<String>()
        val engine = MockEngine { req ->
            assertEquals("Bearer tok", req.headers[HttpHeaders.Authorization])
            assertEquals(HttpMethod.Get, req.method)
            seen += req.url.encodedPath + (req.url.encodedQuery.takeIf { it.isNotEmpty() }?.let { "?$it" } ?: "")
            when (req.url.encodedPath) {
                "/api/v1/instances" -> ok(instances)
                "/api/v1/chores" -> ok("""{"items":[$chore]}""")
                "/api/v1/kids/5/points" -> ok("""{"balance":42,"available":40}""")
                "/api/v1/kids/5/streaks" -> ok("""{"daily":{"current":3,"best":9},"chores":[]}""")
                else -> respondError(HttpStatusCode.NotFound)
            }
        }
        val result = KidChores(engine).today(url, "tok", 5) as Fetch.Ok
        val today = result.value
        assertTrue("/api/v1/instances?kidId=5" in seen)
        assertEquals(10L, today.plan.find(1)?.points)
        assertEquals(ChoreStatusView.REDO, today.plan.find(1)?.status)
        assertEquals("Corners please", today.plan.find(1)?.instance?.lastRejection?.text)
        assertEquals(1, today.plan.planned.size)
        assertEquals(42L, today.balance?.balance)
        assertEquals(3L, today.streak?.current)
    }

    @Test
    fun sideRequestsFailingStillShowTheList() = runTest {
        val engine = MockEngine { req ->
            when (req.url.encodedPath) {
                "/api/v1/instances" -> ok(instances)
                "/api/v1/chores" -> throw IOException("reset")
                else -> respondError(HttpStatusCode.Forbidden)
            }
        }
        val today = (KidChores(engine).today(url, "tok", 5) as Fetch.Ok).value
        assertEquals(2, today.plan.today.sumOf { it.chores.size } + today.plan.planned.sumOf { it.chores.size })
        assertNull(today.plan.find(1)?.points)
        assertNull(today.balance)
        assertNull(today.streak)
    }

    @Test
    fun refusedTokenIsUnauthorized() = runTest {
        val engine = MockEngine { respondError(HttpStatusCode.Unauthorized) }
        assertEquals(Fetch.Unauthorized, KidChores(engine).today(url, "tok", 5))
        assertEquals(Fetch.Unauthorized, KidChores(engine).chore(url, "tok", 7))
    }

    @Test
    fun offlineIsUnreachable() = runTest {
        val engine = MockEngine { throw IOException("no route") }
        assertTrue(KidChores(engine).today(url, "tok", 5) is Fetch.Unreachable)
    }

    @Test
    fun serverErrorFails() = runTest {
        val engine = MockEngine { respondError(HttpStatusCode.InternalServerError) }
        assertEquals(Fetch.Failed("http_500"), KidChores(engine).today(url, "tok", 5))
    }

    @Test
    fun readsAChoreWithStepsAndEvidence() = runTest {
        val engine = MockEngine { req ->
            assertEquals("/api/v1/chores/7", req.url.encodedPath)
            ok(chore)
        }
        val c = (KidChores(engine).chore(url, "tok", 7) as Fetch.Ok).value
        assertEquals(listOf("Straighten sheet", "Fluff pillow"), c.steps)
        assertEquals(
            listOf(EvidenceRequirement.Photos(1, false), EvidenceRequirement.Checklist),
            c.evidence.requirements(),
        )
    }

    @Test
    fun missingChoreIsNotFound() = runTest {
        val engine = MockEngine { respondError(HttpStatusCode.NotFound) }
        assertEquals(Fetch.NotFound, KidChores(engine).chore(url, "tok", 7))
    }
}
