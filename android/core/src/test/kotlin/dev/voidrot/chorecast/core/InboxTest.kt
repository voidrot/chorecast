package dev.voidrot.chorecast.core

import io.ktor.client.engine.mock.MockEngine
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
import java.time.OffsetDateTime

class InboxTest {
    private val url = (ServerUrl.parse("https://chores.example.com") as Result.Ok).value
    private val json = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString())

    private fun item(id: Int, kind: String, data: String = """{"choreName":"Make bed"}""", read: Boolean = false, refKind: String = "instance"): String {
        val readAt = if (read) ""","readAt":"2026-05-04T09:00:00Z"""" else ""
        return """{"id":$id,"kind":"$kind","refKind":"$refKind","refId":${id * 10},"kidId":5,"data":$data,"createdAt":"2026-05-04T08:00:00Z"$readAt}"""
    }

    private fun page(vararg items: String, next: String? = null, unread: Int = 0): String {
        val cursor = next?.let { """"nextCursor":"$it",""" }.orEmpty()
        return """{"items":[${items.joinToString(",")}],$cursor"unreadCount":$unread}"""
    }

    private fun entry(id: Long, read: Boolean = false) =
        InboxEntry(id, InboxMessage.Other, OffsetDateTime.parse("2026-05-04T08:00:00Z"), read, null, null)

    private suspend fun load(body: String): InboxFeed {
        val engine = MockEngine { req ->
            assertEquals("Bearer tok", req.headers[HttpHeaders.Authorization])
            respond(body, HttpStatusCode.OK, json)
        }
        return (InboxClient(engine).list(url, "tok") as Fetch.Ok).value
    }

    @Test
    fun mapsEachKindToMessageParameters() = runTest {
        val feed = load(
            page(
                item(1, "chore_submitted"),
                item(2, "chore_approved", """{"choreName":"Make bed","auto":true}"""),
                item(3, "chore_rejected", """{"choreName":"Make bed","reasonCode":"needs_redo","reasonText":"Corners"}"""),
                item(4, "chore_missed"),
                item(5, "chore_excused"),
                item(6, "streak_milestone", """{"days":7,"points":0}""", refKind = "points_entry"),
                unread = 6,
            ),
        )
        val m = feed.entries.map { it.message }
        assertEquals(InboxMessage.Submitted("Make bed"), m[0])
        assertEquals(InboxMessage.Approved("Make bed", auto = true), m[1])
        assertEquals(InboxMessage.Rejected("Make bed", RejectionReason.NEEDS_REDO, "Corners"), m[2])
        assertEquals(InboxMessage.Missed("Make bed"), m[3])
        assertEquals(InboxMessage.Excused("Make bed"), m[4])
        assertEquals(InboxMessage.Streak(7, 0), m[5])
        assertEquals(10L, feed.entries[0].instanceId)
        assertNull("a points entry is not a chore", feed.entries[5].instanceId)
        assertEquals(6L, feed.unread)
    }

    @Test
    fun unknownReasonCodeAndBlankTextMapToNull() = runTest {
        val feed = load(page(item(1, "chore_rejected", """{"choreName":"x","reasonCode":"brand_new","reasonText":" "}""")))
        assertEquals(InboxMessage.Rejected("x", null, null), feed.entries[0].message)
    }

    @Test
    fun readFlagComesFromReadAt() = runTest {
        val feed = load(page(item(1, "chore_missed", read = true), item(2, "chore_missed")))
        assertEquals(listOf(true, false), feed.entries.map { it.read })
    }

    @Test
    fun listSendsCursorAndKeepsNextCursor() = runTest {
        var query = ""
        val engine = MockEngine { req ->
            query = req.url.encodedQuery
            respond(page(item(1, "chore_missed"), next = "c2"), HttpStatusCode.OK, json)
        }
        val feed = (InboxClient(engine).list(url, "tok", cursor = "c1") as Fetch.Ok).value
        assertTrue("cursor=c1" in query)
        assertEquals("c2", feed.nextCursor)
    }

    @Test
    fun appendSkipsOverlapAndTakesNewCursorAndCount() {
        val merged = InboxFeed(listOf(entry(5), entry(4)), "c1", 4).append(InboxFeed(listOf(entry(4), entry(3)), null, 3))
        assertEquals(listOf(5L, 4L, 3L), merged.entries.map { it.id })
        assertNull(merged.nextCursor)
        assertEquals(3L, merged.unread)
    }

    @Test
    fun markReadDropsTheCountOnlyForAnUnreadEntry() {
        val feed = InboxFeed(listOf(entry(2), entry(1, read = true)), null, 1)
        val after = feed.markRead(2)
        assertEquals(0L, after.unread)
        assertTrue(after.entries.all { it.read })
        assertEquals("already read: no change", after, after.markRead(2))
        assertEquals("read entry does not lower the count", feed, feed.markRead(1))
        assertEquals("unknown id: no change", feed, feed.markRead(99))
        assertEquals("never below zero", 0L, InboxFeed(listOf(entry(1)), null, 0).markRead(1).unread)
    }

    @Test
    fun markAllReadClearsTheCountAndKeepsTheCursor() {
        val after = InboxFeed(listOf(entry(1)), "c", 12).markAllRead()
        assertEquals(0L, after.unread)
        assertTrue(after.entries.all { it.read })
        assertEquals("c", after.nextCursor)
    }

    @Test
    fun markReadAndMarkAllReadPostToTheirPaths() = runTest {
        val seen = mutableListOf<Pair<HttpMethod, String>>()
        val engine = MockEngine { req ->
            seen += req.method to req.url.encodedPath
            respond("", HttpStatusCode.NoContent)
        }
        assertEquals(Fetch.Ok(Unit), InboxClient(engine).markRead(url, "tok", 7))
        assertEquals(Fetch.Ok(Unit), InboxClient(engine).markAllRead(url, "tok"))
        assertEquals(
            listOf(HttpMethod.Post to "/api/v1/inbox/7/read", HttpMethod.Post to "/api/v1/inbox/read-all"),
            seen,
        )
    }

    @Test
    fun mapsFailures() = runTest {
        fun client(status: HttpStatusCode) = InboxClient(MockEngine { respondError(status) })
        assertEquals(Fetch.Unauthorized, client(HttpStatusCode.Unauthorized).list(url, "tok"))
        assertEquals(Fetch.Failed("http_403"), client(HttpStatusCode.Forbidden).list(url, "tok"))
        assertEquals(Fetch.NotFound, client(HttpStatusCode.NotFound).markRead(url, "tok", 1))
        assertEquals(Fetch.Failed("http_500"), client(HttpStatusCode.InternalServerError).markAllRead(url, "tok"))
        val down = InboxClient(MockEngine { throw IOException("no route") })
        assertEquals(Fetch.Unreachable("no route"), down.list(url, "tok"))
    }
}
