package dev.voidrot.chorecast.core

import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.engine.mock.toByteArray
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException
import java.time.Instant

class CreatePairingTest {
    private val url = (ServerUrl.parse("http://192.168.1.20:8080") as Result.Ok).value
    private val json = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString())

    @Test
    fun createsACodeForAKidAndForASharedDevice() = runTest {
        val bodies = mutableListOf<String>()
        val engine = MockEngine { req ->
            assertEquals("http://192.168.1.20:8080/api/v1/devices/pairing", req.url.toString())
            assertEquals("Bearer parent", req.headers[HttpHeaders.Authorization])
            bodies += String(req.body.toByteArray())
            respond(
                """{"code":"k7mp-q2xd","server":"http://192.168.1.20:8080","qr":"x","expiresAt":"2026-11-02T10:05:00Z","kidId":7}""",
                HttpStatusCode.OK, json,
            )
        }
        val res = DeviceClient(engine).createPairing(url, "parent", 7)
        assertTrue(res is CreatePairingResult.Created)
        res as CreatePairingResult.Created
        assertEquals(PairingQr(url, "k7mp-q2xd"), res.pairing.qr)
        assertEquals(Instant.parse("2026-11-02T10:05:00Z"), res.pairing.expiresAt)
        DeviceClient(engine).createPairing(url, "parent", null)
        assertTrue(bodies[0].contains("\"kid\":\"7\""))
        assertFalse(bodies[1].contains("kid"))
    }

    @Test
    fun mapsAnExpiredSessionAndOtherFailures() = runTest {
        fun failing(status: HttpStatusCode, code: String) = MockEngine {
            respond(
                """{"status":${status.value},"code":"$code"}""", status,
                headersOf(HttpHeaders.ContentType, "application/problem+json"),
            )
        }
        assertEquals(CreatePairingResult.SignedOut, DeviceClient(failing(HttpStatusCode.Unauthorized, "unauthorized")).createPairing(url, "p", null))
        assertEquals(CreatePairingResult.Failed("kid_not_found"), DeviceClient(failing(HttpStatusCode.NotFound, "kid_not_found")).createPairing(url, "p", 9))
    }

    @Test
    fun rejectsAServerAddressTheAppCannotUse() = runTest {
        val engine = MockEngine {
            respond("""{"code":"k7mp-q2xd","server":"ftp://x","qr":"x","expiresAt":"2026-11-02T10:05:00Z"}""", HttpStatusCode.OK, json)
        }
        assertEquals(CreatePairingResult.Failed("bad_server_url"), DeviceClient(engine).createPairing(url, "p", null))
    }

    @Test
    fun listsKidsAndSignalsAnExpiredSession() = runTest {
        val ok = MockEngine { req ->
            assertEquals("http://192.168.1.20:8080/api/v1/kids", req.url.toString())
            respond(
                """{"items":[{"id":7,"displayName":"Ada","slug":"ada","color":"#fff","pinRequired":false,"pinSet":false}]}""",
                HttpStatusCode.OK, json,
            )
        }
        assertEquals(listOf(7L), DeviceClient(ok).kids(url, "p")?.map { it.id })
        for (status in listOf(HttpStatusCode.Unauthorized, HttpStatusCode.Forbidden)) {
            assertNull(DeviceClient(MockEngine { respond("", status) }).kids(url, "p"))
        }
    }

    @Test
    fun kidsFailuresSurfaceAsIOException() = runTest {
        suspend fun fails(engine: MockEngine) {
            try {
                DeviceClient(engine).kids(url, "p")
                throw AssertionError("expected IOException")
            } catch (_: IOException) {
            }
        }
        fails(MockEngine { respond("", HttpStatusCode.InternalServerError) })
        fails(MockEngine { throw IOException("offline") })
        // A body that can't be read must not escape as some other exception.
        fails(MockEngine { respond("not json", HttpStatusCode.OK, json) })
    }
}
