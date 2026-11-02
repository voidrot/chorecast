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
import java.time.LocalDate

class KidChoresTest {
    private val url = (ServerUrl.parse("https://chores.example.com") as Result.Ok).value
    private val json = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString())

    private val instances = """{"items":[
        {"id":1,"choreId":7,"choreName":"Make bed","date":"2026-05-04","dueAt":"2026-05-04T08:00:00-05:00","slot":"08:00",
         "status":"open","planned":false,"rejectionCount":1,"points":10,
         "steps":["Straighten sheet","Fluff pillow"],"evidence":{"photos":1,"checklist":true},
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
                "/api/v1/kids/5/points" -> ok("""{"balance":42,"available":40}""")
                "/api/v1/kids/5/streaks" -> ok("""{"daily":{"current":3,"best":9},"chores":[]}""")
                else -> respondError(HttpStatusCode.NotFound)
            }
        }
        val result = KidChores(engine).today(url, "tok", 5) as Fetch.Ok
        val today = result.value
        assertTrue("/api/v1/instances?kidId=5" in seen)
        assertEquals(10L, today.plan.find(1)?.points)
        assertEquals(listOf("Straighten sheet", "Fluff pillow"), today.plan.find(1)?.instance?.steps)
        assertTrue(seen.none { it.startsWith("/api/v1/chores") })
        assertEquals(ChoreStatusView.REDO, today.plan.find(1)?.status)
        assertEquals("Corners please", today.plan.find(1)?.instance?.lastRejection?.text)
        assertEquals(1, today.plan.planned.size)
        assertEquals(42L, today.balance?.balance)
        assertEquals(3L, today.streak?.current)
    }

    @Test
    fun balanceAndStreakFailingStillShowTheList() = runTest {
        val engine = MockEngine { req ->
            when (req.url.encodedPath) {
                "/api/v1/instances" -> ok(instances)
                else -> respondError(HttpStatusCode.Forbidden)
            }
        }
        val today = (KidChores(engine).today(url, "tok", 5) as Fetch.Ok).value
        assertEquals(2, today.plan.today.sumOf { it.chores.size } + today.plan.planned.sumOf { it.chores.size })
        assertNull(today.balance)
        assertNull(today.streak)
    }

    @Test
    fun refusedTokenIsUnauthorized() = runTest {
        val engine = MockEngine { respondError(HttpStatusCode.Unauthorized) }
        assertEquals(Fetch.Unauthorized, KidChores(engine).today(url, "tok", 5))
        assertEquals(Fetch.Unauthorized, KidChores(engine).evidence(url, "tok", 1))
        val range = DateRange(LocalDate.of(2026, 5, 1), LocalDate.of(2026, 5, 4))
        assertEquals(Fetch.Unauthorized, KidChores(engine).instancesIn(url, "tok", 5, range))
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
    fun readsInstancesForADateRange() = runTest {
        val engine = MockEngine { req ->
            assertEquals("/api/v1/instances", req.url.encodedPath)
            assertEquals("5", req.url.parameters["kidId"])
            assertEquals("2026-03-05", req.url.parameters["from"])
            assertEquals("2026-05-05", req.url.parameters["to"])
            ok(instances)
        }
        val range = latestHistoryRange(LocalDate.of(2026, 5, 4))
        val items = (KidChores(engine).instancesIn(url, "tok", 5, range) as Fetch.Ok).value
        assertEquals(listOf(1L, 2L), items.map { it.id })
    }

    @Test
    fun readsEvidenceWithExpiredPhotos() = runTest {
        val engine = MockEngine { req ->
            assertEquals("/api/v1/instances/1/evidence", req.url.encodedPath)
            ok(
                """{"items":[{"attempt":1,"checklist":[0],"note":"done","submittedAt":"2026-05-04T09:00:00Z","photos":[
                {"mediaId":1,"url":"https://x.example/m/1?sig=a","urlExpiresAt":"2026-05-04T09:10:00Z"},
                {"mediaId":2,"expired":true}]}]}""",
            )
        }
        val items = (KidChores(engine).evidence(url, "tok", 1) as Fetch.Ok).value
        val view = items.latestView(listOf("Straighten sheet", "Fluff pillow"))!!
        assertEquals(listOf(true, false), view.steps.map { it.done })
        assertEquals(listOf(PhotoView.Available("https://x.example/m/1?sig=a", null), PhotoView.Expired(null)), view.photos)
    }

    @Test
    fun missingEvidenceIsNotFound() = runTest {
        val engine = MockEngine { respondError(HttpStatusCode.NotFound) }
        assertEquals(Fetch.NotFound, KidChores(engine).evidence(url, "tok", 7))
    }

    @Test
    fun upcomingAndProfileReadOnlyWhatRemindersNeed() = runTest {
        val seen = mutableListOf<String>()
        val engine = MockEngine { req ->
            seen += req.url.encodedPath
            when (req.url.encodedPath) {
                "/api/v1/instances" -> ok(instances)
                "/api/v1/kids/5" -> ok("""{"id":5,"slug":"sam","displayName":"Sam","color":"#ffffff","pinRequired":false,
                    "pinSet":false,"dayEndOverride":"19:30","quietHours":{"start":"21:00","end":"06:30"}}""")
                else -> respondError(HttpStatusCode.NotFound)
            }
        }
        val client = KidChores(engine)
        assertEquals(2, (client.upcoming(url, "tok", 5) as Fetch.Ok).value.size)
        val kid = (client.profile(url, "tok", 5) as Fetch.Ok).value
        assertEquals("19:30", kid.dayEndOverride)
        assertEquals("06:30", kid.quietHours?.end)
        assertEquals(listOf("/api/v1/instances", "/api/v1/kids/5"), seen)
    }

    @Test
    fun upcomingMapsARefusedToken() = runTest {
        val engine = MockEngine { respondError(HttpStatusCode.Unauthorized) }
        assertEquals(Fetch.Unauthorized, KidChores(engine).upcoming(url, "tok", 5))
    }
}
