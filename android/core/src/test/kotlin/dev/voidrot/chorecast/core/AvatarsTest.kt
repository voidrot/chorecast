package dev.voidrot.chorecast.core

import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class AvatarsTest {
    @Test
    fun readsTheBundledAvatarNumber() {
        assertEquals(1, bundledAvatarNumber("pixel-01"))
        assertEquals(24, bundledAvatarNumber("pixel-24"))
        assertNull(bundledAvatarNumber("pixel-25"))
        assertNull(bundledAvatarNumber("pixel-00"))
        assertNull(bundledAvatarNumber("robot-01"))
        assertNull(bundledAvatarNumber(null))
    }

    @Test
    fun fetchesThePhotoFromTheSignedLinkWithoutCredentials() = runTest {
        val bytes = byteArrayOf(1, 2, 3)
        val engine = MockEngine { req ->
            assertEquals("https://chores.example/media/1?sig=abc", req.url.toString())
            assertNull(req.headers[HttpHeaders.Authorization])
            respond(bytes, HttpStatusCode.OK)
        }
        assertArrayEquals(bytes, AvatarPhotos(engine).fetch("https://chores.example/media/1?sig=abc"))
    }

    @Test
    fun expiredOrOversizedOrUnreachableMeansNoPhoto() = runTest {
        assertNull(AvatarPhotos(MockEngine { respond("", HttpStatusCode.Forbidden) }).fetch("https://x/a"))
        val big = MockEngine { respond(ByteArray(10), HttpStatusCode.OK, headersOf(HttpHeaders.ContentLength, "${AvatarPhotos.MAX_BYTES + 1}")) }
        assertNull(AvatarPhotos(big).fetch("https://x/a"))
        // No Content-Length (chunked): the cap still holds.
        val chunked = MockEngine { respond(ByteArray((AvatarPhotos.MAX_BYTES + 1000).toInt()), HttpStatusCode.OK) }
        assertNull(AvatarPhotos(chunked).fetch("https://x/a"))
        val atCap = MockEngine { respond(ByteArray(AvatarPhotos.MAX_BYTES.toInt()), HttpStatusCode.OK) }
        assertEquals(AvatarPhotos.MAX_BYTES.toInt(), AvatarPhotos(atCap).fetch("https://x/a")?.size)
        assertNull(AvatarPhotos(MockEngine { throw java.io.IOException("offline") }).fetch("https://x/a"))
    }
}
