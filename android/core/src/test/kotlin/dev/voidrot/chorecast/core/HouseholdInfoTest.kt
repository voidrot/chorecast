package dev.voidrot.chorecast.core

import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class HouseholdInfoTest {
    private val url = (ServerUrl.parse("https://chores.example.com") as Result.Ok).value

    @Test
    fun readsSettings() = runTest {
        val engine = MockEngine { req ->
            assertEquals("Bearer tok", req.headers[HttpHeaders.Authorization])
            respond(
                """{"name":"The Bradys","timezone":"America/Chicago","dayEnd":"21:00","weekStart":"sunday",
                   "allowNegativeBalance":false,"siblingVisibility":"private","mediaRetentionDays":30,
                   "offlineGraceMinutes":120,"defaultChoreMinutes":10}""",
                HttpStatusCode.OK,
                headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString()),
            )
        }
        val s = HouseholdInfo(engine).get(url, "tok")
        assertEquals("The Bradys", s?.name)
        assertEquals("21:00", s?.dayEnd)
        assertNull(s?.autoApproveHours)
    }

    @Test
    fun nullWhenRefused() = runTest {
        val engine = MockEngine { respond("{}", HttpStatusCode.Unauthorized) }
        assertNull(HouseholdInfo(engine).get(url, "tok"))
    }
}
