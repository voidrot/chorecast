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
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class DevicesTest {
    private val url = (ServerUrl.parse("http://192.168.1.20:8080") as Result.Ok).value
    private val json = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString())
    private fun problem(status: HttpStatusCode, code: String, vararg extra: Pair<String, String>) = MockEngine {
        respond(
            """{"status":${status.value},"code":"$code"}""",
            status,
            headersOf(*(listOf(HttpHeaders.ContentType to listOf("application/problem+json")) + extra.map { it.first to listOf(it.second) }).toTypedArray()),
        )
    }

    @Test
    fun parsesPairingQr() {
        assertEquals(PairingQr(url, "k7mp-q2xd"), PairingQr.parse("""{"server":"http://192.168.1.20:8080","code":"k7mp-q2xd"}"""))
        assertNull(PairingQr.parse("https://example.com"))
        assertNull(PairingQr.parse("""{"server":"ftp://x","code":"a"}"""))
        assertNull(PairingQr.parse("""{"server":"http://x"}"""))
    }

    @Test
    fun pairsAndDecodesDates() = runTest {
        val engine = MockEngine { req ->
            assertEquals("http://192.168.1.20:8080/api/v1/devices/pair", req.url.toString())
            respond(
                """{"token":"dev","device":{"id":4,"name":"Pixel Tablet","shared":true,"createdAt":"2026-10-31T12:00:00.123Z"}}""",
                HttpStatusCode.OK, json,
            )
        }
        val res = DeviceClient(engine).pair(url, " k7mp-q2xd ", "Pixel Tablet", "0.1.0")
        assertTrue(res is PairResult.Paired)
        res as PairResult.Paired
        assertEquals("dev", res.token)
        assertTrue(res.device.shared)
        assertEquals(Instant.parse("2026-10-31T12:00:00.123Z"), res.device.createdAt.toInstant())

        assertEquals(PairResult.InvalidCode, DeviceClient(problem(HttpStatusCode.BadRequest, "invalid_pairing_code")).pair(url, "x", "n", "v"))
    }

    @Test
    fun sessionsAndElevation() = runTest {
        val ok = MockEngine { req ->
            assertEquals("Bearer dev", req.headers[HttpHeaders.Authorization])
            respond("""{"token":"sess","userId":7,"expiresAt":"2026-10-31T12:02:00Z"}""", HttpStatusCode.OK, json)
        }
        assertEquals(
            SessionResult.Started(DeviceSessionToken("sess", 7, Instant.parse("2026-10-31T12:02:00Z"))),
            DeviceClient(ok).elevate(url, "dev", 7, "1357"),
        )
        assertEquals(SessionResult.WrongPin, DeviceClient(problem(HttpStatusCode.Forbidden, "invalid_pin")).startKidSession(url, "dev", 3, "0000"))
        assertEquals(
            SessionResult.Locked(900),
            DeviceClient(problem(HttpStatusCode.TooManyRequests, "pin_locked", "Retry-After" to "900")).elevate(url, "dev", 7, "0"),
        )
        assertEquals(SessionResult.Unpaired, DeviceClient(problem(HttpStatusCode.Unauthorized, "unauthenticated")).startKidSession(url, "dev", 3, null))
    }

    @Test
    fun pickerNullWhenUnpaired() = runTest {
        assertNull(DeviceClient(problem(HttpStatusCode.Unauthorized, "unauthenticated")).picker(url, "dev"))
        val picker = DeviceClient(MockEngine {
            respond(
                """{"device":{"id":4,"name":"Tab","shared":true,"createdAt":"2026-10-31T12:00:00Z"},
                   "kids":[{"id":3,"slug":"sam","displayName":"Sam","color":"#3b82f6","pinRequired":true}],
                   "parents":[{"id":1,"displayName":"Mom"}]}""",
                HttpStatusCode.OK, json,
            )
        }).picker(url, "dev")
        assertEquals("Sam", picker?.kids?.single()?.displayName)
        assertEquals("Mom", picker?.parents?.single()?.displayName)
    }
}
