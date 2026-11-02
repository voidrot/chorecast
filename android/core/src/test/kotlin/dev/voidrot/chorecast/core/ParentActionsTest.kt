package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.Kid
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
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException
import java.time.Instant
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.ZoneOffset

class ParentActionsTest {
    private val url = (ServerUrl.parse("https://chores.example.com") as Result.Ok).value
    private val json = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString())
    private val today = LocalDate.parse("2026-05-04")

    private fun MockRequestHandleScope.reply(status: HttpStatusCode, body: String) = respond(body, status, json)
    private fun problem(code: String) = """{"title":"x","status":409,"code":"$code"}"""

    private fun inst(
        id: Long,
        status: ChoreInstance.Status,
        kidId: Long? = 1,
        date: LocalDate = today,
        planned: Boolean = false,
        group: List<Long>? = null,
    ) = ChoreInstance(
        choreId = 7,
        choreName = "Bed",
        date = date,
        dueAt = OffsetDateTime.of(2026, 5, 4, 20, 0, 0, 0, ZoneOffset.UTC),
        id = id,
        planned = planned,
        status = status,
        kidId = kidId,
        groupKidIds = group,
    )

    private fun kid(id: Long) = Kid(color = "#fff", displayName = "K$id", id = id, pinRequired = false, pinSet = false, slug = "k$id")

    private val instanceJson = """{"id":1,"choreId":7,"choreName":"Bed","date":"2026-05-04",
        "dueAt":"2026-05-04T20:00:00Z","status":"excused","planned":false}"""

    // --- action availability ---

    @Test
    fun actionsFollowTheStatus() {
        assertEquals(listOf(ParentAction.REVERSE), actionsFor(inst(1, ChoreInstance.Status.APPROVED)))
        assertEquals(listOf(ParentAction.EXCUSE), actionsFor(inst(1, ChoreInstance.Status.MISSED)))
        assertEquals(listOf(ParentAction.REASSIGN), actionsFor(inst(1, ChoreInstance.Status.OPEN)))
        assertEquals(emptyList<ParentAction>(), actionsFor(inst(1, ChoreInstance.Status.SUBMITTED)))
        assertEquals(emptyList<ParentAction>(), actionsFor(inst(1, ChoreInstance.Status.EXCUSED)))
    }

    @Test
    fun aSharedOpenChoreCannotBeReassigned() {
        assertEquals(emptyList<ParentAction>(), actionsFor(inst(1, ChoreInstance.Status.OPEN, kidId = null, group = listOf(1, 2))))
    }

    @Test
    fun reassignTargetsLeaveOutTheCurrentAssignee() {
        val targets = reassignTargets(listOf(kid(1), kid(2), kid(3)), inst(1, ChoreInstance.Status.OPEN, kidId = 2))
        assertEquals(listOf(1L, 3L), targets.map { it.id })
    }

    // --- adjustment validation ---

    private fun valid(subtract: Boolean, amount: String, note: String) =
        (buildAdjustment(subtract, amount, note) as AdjustmentCheck.Valid).body

    private fun problemOf(amount: String, note: String) =
        (buildAdjustment(false, amount, note) as AdjustmentCheck.Invalid).problem

    @Test
    fun adjustmentSignComesFromTheChoiceAndTheNoteIsTrimmed() {
        assertEquals(5L, valid(false, " 5 ", "  well done ").amount)
        assertEquals(-5L, valid(true, "5", "x").amount)
        assertEquals("well done", valid(false, "5", "  well done ").note)
    }

    @Test
    fun adjustmentAmountMustBeAWholeNumberFromOneToAMillion() {
        for (bad in listOf("", " ", "0", "-3", "+3", "2.5", "1e3", "abc", "1000001", "99999999999999999999")) {
            assertEquals(bad, AdjustmentProblem.AMOUNT_INVALID, problemOf(bad, "n"))
        }
        assertEquals(1_000_000L, valid(false, "1000000", "n").amount)
        assertEquals(1L, valid(false, "1", "n").amount)
    }

    @Test
    fun adjustmentNoteIsRequiredAndLimitedInCodePoints() {
        assertEquals(AdjustmentProblem.NOTE_REQUIRED, problemOf("5", "   "))
        val atLimit = "😀".repeat(NOTE_MAX) // 1000 UTF-16 units, 500 code points
        assertEquals(atLimit, valid(false, "5", atLimit).note)
        assertEquals(AdjustmentProblem.NOTE_TOO_LONG, problemOf("5", atLimit + "a"))
    }

    @Test
    fun actionNoteIsAlwaysABodyAndIsCapped() {
        assertEquals(null, buildActionNote("   ").note)
        assertEquals("ok", buildActionNote(" ok ").note)
        assertEquals(NOTE_MAX, buildActionNote("a".repeat(NOTE_MAX + 20)).note!!.length)
    }

    // --- error and 409 mapping ---

    @Test
    fun failuresMapToResults() {
        assertEquals(ActionResult.Unauthorized, actionFailureFor(401, "unauthorized"))
        assertEquals(ActionResult.Forbidden, actionFailureFor(403, "forbidden"))
        assertEquals(ActionResult.Invalid("validation_failed"), actionFailureFor(422, "validation_failed"))
        for (code in listOf("instance_closed", "instance_taken", "instance_shared", "instance_not_open", "instance_not_missed", "instance_not_approved")) {
            assertEquals(ActionResult.Stale(code), actionFailureFor(409, code))
        }
        assertEquals(ActionResult.Stale("instance_not_found"), actionFailureFor(404, "instance_not_found"))
        // A key whose first request is still running is not a stale chore, and the outcome is unknown.
        val running = actionFailureFor(409, "idempotency_key_in_progress")
        assertEquals(ActionResult.Failed("idempotency_key_in_progress"), running)
        assertTrue(!running.outcomeKnown)
        assertTrue(!actionFailureFor(500, "http_500").outcomeKnown)
        assertTrue(actionFailureFor(409, "instance_closed").outcomeKnown)
    }

    @Test
    fun staleReasonsNameTheReassignConflicts() {
        assertEquals(StaleReason.CLOSED, staleReasonFor("instance_closed"))
        assertEquals(StaleReason.TAKEN, staleReasonFor("instance_taken"))
        assertEquals(StaleReason.SHARED, staleReasonFor("instance_shared"))
        assertEquals(StaleReason.NOT_FOUND, staleReasonFor("instance_not_found"))
        assertEquals(StaleReason.CHANGED, staleReasonFor("instance_not_missed"))
    }

    // --- request building ---

    @Test
    fun adjustmentPostsASignedAmountAndNoteWithAKey() = runTest {
        val seen = mutableListOf<HttpRequestData>()
        var body = ""
        val client = ParentReview(MockEngine { req ->
            seen += req
            body = String(req.body.toByteArray())
            reply(
                HttpStatusCode.Created,
                """{"amount":-5,"balanceAfter":10,"createdAt":"2026-05-04T10:00:00Z","id":9,"kidId":3,
                    "kind":"adjustment","localDate":"2026-05-04","note":"broke a plate"}""",
            )
        })
        val adjustment = valid(true, "5", "broke a plate")
        val result = client.adjust(url, "tok", 3, adjustment)
        assertEquals(9L, (result as ActionResult.Done).value.id)
        val req = seen.single()
        assertEquals(HttpMethod.Post, req.method)
        assertEquals("/api/v1/kids/3/points/adjustments", req.url.encodedPath)
        assertEquals("Bearer tok", req.headers[HttpHeaders.Authorization])
        assertTrue(!req.headers["Idempotency-Key"].isNullOrBlank())
        assertTrue(body.contains("\"amount\":-5"))
        assertTrue(body.contains("\"note\":\"broke a plate\""))
    }

    @Test
    fun excuseAndReversalSendAJsonBodyEvenWithoutANote() = runTest {
        val seen = mutableListOf<HttpRequestData>()
        val bodies = mutableListOf<String>()
        val client = ParentReview(MockEngine { req ->
            seen += req
            bodies += String(req.body.toByteArray())
            reply(HttpStatusCode.OK, instanceJson)
        })
        assertTrue(client.excuse(url, "tok", 4, buildActionNote("")) is ActionResult.Done)
        assertTrue(client.reverseApproval(url, "tok", 5, buildActionNote("oops")) is ActionResult.Done)
        assertEquals("/api/v1/instances/4/excusal", seen[0].url.encodedPath)
        assertEquals("/api/v1/instances/5/approval/reversal", seen[1].url.encodedPath)
        for (req in seen) {
            assertTrue(!req.headers["Idempotency-Key"].isNullOrBlank())
            assertTrue(req.body.contentType?.match(ContentType.Application.Json) == true)
        }
        assertEquals("{}", bodies[0])
        assertEquals("""{"note":"oops"}""", bodies[1])
    }

    @Test
    fun reassignPostsTheTargetKid() = runTest {
        var path = ""
        var body = ""
        val client = ParentReview(MockEngine { req ->
            path = req.url.encodedPath
            body = String(req.body.toByteArray())
            reply(HttpStatusCode.OK, instanceJson)
        })
        assertTrue(client.reassign(url, "tok", 8, 2) is ActionResult.Done)
        assertEquals("/api/v1/instances/8/reassignment", path)
        assertEquals("""{"kidId":2}""", body)
    }

    @Test
    fun reassignConflictsSurfaceAsStaleWithTheirCode() = runTest {
        for (code in listOf("instance_closed", "instance_taken", "instance_shared")) {
            val client = ParentReview(MockEngine { reply(HttpStatusCode.Conflict, problem(code)) })
            assertEquals(ActionResult.Stale(code), client.reassign(url, "tok", 8, 2))
        }
    }

    @Test
    fun kidDayAsksForTheKidsRangeAndNeedsTheKid() = runTest {
        val queries = mutableListOf<String>()
        val kidJson = """{"id":2,"slug":"ben","displayName":"Ben","color":"#fff","pinRequired":false,"pinSet":false}"""
        val client = ParentReview(MockEngine { req ->
            when {
                req.url.encodedPath.endsWith("/instances") -> {
                    queries += req.url.encodedQuery
                    reply(HttpStatusCode.OK, """{"items":[$instanceJson]}""")
                }
                req.url.encodedPath.endsWith("/kids") -> reply(HttpStatusCode.OK, """{"items":[$kidJson]}""")
                else -> reply(HttpStatusCode.NotFound, problem("kid_not_found"))
            }
        })
        val day = (client.kidDay(url, "tok", 2, today.minusDays(KID_DAY_LOOKBACK), today) as Fetch.Ok).value
        assertEquals("Ben", day.kid.displayName)
        assertEquals(null, day.balance)
        assertEquals(1, day.instances.size)
        assertTrue(queries.single().contains("kidId=2"))
        assertTrue(queries.single().contains("from=2026-04-27"))
        assertTrue(queries.single().contains("to=2026-05-04"))
        assertEquals(Fetch.NotFound, client.kidDay(url, "tok", 99, today, today))
    }

    // --- idempotency keys ---

    @Test
    fun aRetryAfterATimeoutReusesTheAdjustmentKeyAndANewSubmissionGetsANewOne() = runTest {
        val keys = mutableListOf<String?>()
        var calls = 0
        val entry = """{"amount":5,"balanceAfter":5,"createdAt":"2026-05-04T10:00:00Z","id":9,"kidId":3,
            "kind":"adjustment","localDate":"2026-05-04","note":"n"}"""
        val client = ParentReview(MockEngine { req ->
            keys += req.headers["Idempotency-Key"]
            if (++calls == 1) throw IOException("timeout")
            reply(HttpStatusCode.Created, entry)
        })
        val body = valid(false, "5", "n")
        assertTrue(client.adjust(url, "tok", 3, body) is ActionResult.Unreachable)
        assertTrue(client.adjust(url, "tok", 3, body) is ActionResult.Done)
        assertTrue(client.adjust(url, "tok", 3, body) is ActionResult.Done)
        assertEquals(keys[0], keys[1])
        assertNotEquals(keys[1], keys[2])
    }

    @Test
    fun anEditedAdjustmentGetsAFreshKey() = runTest {
        val keys = mutableListOf<String?>()
        val client = ParentReview(MockEngine { req ->
            keys += req.headers["Idempotency-Key"]
            throw IOException("down")
        })
        client.adjust(url, "tok", 3, valid(false, "5", "n"))
        client.adjust(url, "tok", 3, valid(false, "6", "n"))
        client.adjust(url, "tok", 3, valid(false, "5", "n"))
        assertNotEquals(keys[0], keys[1])
        assertEquals(keys[0], keys[2])
    }

    @Test
    fun aReloadDoesNotForgetAnUnansweredAdjustmentKey() = runTest {
        val keys = mutableListOf<String?>()
        var down = true
        val kidJson = """{"id":3,"slug":"c","displayName":"C","color":"#fff","pinRequired":false,"pinSet":false}"""
        val client = ParentReview(MockEngine { req ->
            val path = req.url.encodedPath
            when {
                path.endsWith("/adjustments") -> {
                    keys += req.headers["Idempotency-Key"]
                    if (down) throw IOException("timeout")
                    reply(HttpStatusCode.InternalServerError, "")
                }
                path.endsWith("/instances") -> reply(HttpStatusCode.OK, """{"items":[]}""")
                path.endsWith("/kids") -> reply(HttpStatusCode.OK, """{"items":[$kidJson]}""")
                else -> reply(HttpStatusCode.NotFound, problem("x"))
            }
        })
        val body = valid(false, "5", "n")
        client.adjust(url, "tok", 3, body)
        client.kidDay(url, "tok", 3, today, today)
        down = false
        client.adjust(url, "tok", 3, body)
        assertEquals(keys[0], keys[1])
    }

    // --- the kid's day ---

    @Test
    fun householdTodayFollowsTheDayEnd() {
        val zone = ZoneId.of("UTC")
        val at2230 = Instant.parse("2026-05-04T22:30:00Z")
        assertEquals(LocalDate.parse("2026-05-04"), householdToday(zone, "00:00", at2230))
        assertEquals(LocalDate.parse("2026-05-05"), householdToday(zone, "21:00", at2230))
        assertEquals(LocalDate.parse("2026-05-05"), householdToday(zone, "22:30", at2230))
        assertEquals(LocalDate.parse("2026-05-04"), householdToday(zone, "garbage", at2230))
        assertEquals(LocalDate.parse("2026-05-04"), householdToday(ZoneId.of("Pacific/Auckland"), "24:00", Instant.parse("2026-05-03T20:00:00Z")))
    }

    @Test
    fun theKidDayGroupsByWhatNeedsAParent() {
        val yesterday = today.minusDays(1)
        val plan = planKidDay(
            1,
            listOf(
                inst(1, ChoreInstance.Status.APPROVED, date = yesterday),
                inst(2, ChoreInstance.Status.MISSED, date = yesterday),
                inst(3, ChoreInstance.Status.SUBMITTED),
                inst(4, ChoreInstance.Status.OPEN),
                inst(5, ChoreInstance.Status.APPROVED),
                inst(6, ChoreInstance.Status.OPEN, date = today.plusDays(1), planned = true),
                inst(7, ChoreInstance.Status.OPEN, kidId = 2),
                inst(8, ChoreInstance.Status.OPEN, kidId = null, group = listOf(1, 2)),
            ),
            today,
        )
        assertEquals(listOf(2L, 3L), plan.attention.map { it.id })
        assertEquals(listOf(4L, 5L, 8L), plan.today.map { it.id })
        assertEquals(listOf(1L), plan.earlier.map { it.id })
    }
}
