package dev.voidrot.chorecast.core

import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

class ServerCheckerTest {
    private val url = (ServerUrl.parse("https://chores.example.com/sub") as Result.Ok).value
    private val json = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString())

    private fun engine(status: HttpStatusCode = HttpStatusCode.OK, body: String, headers: io.ktor.http.Headers = json, seen: MutableList<String>? = null) =
        MockEngine { req ->
            seen?.add(req.url.toString())
            respond(body, status, headers)
        }

    @Test
    fun readyAndIgnoresUnknownFields() = runTest {
        val seen = mutableListOf<String>()
        val body = """{"version":"0.3.0","commit":"abc","apiVersion":1,"oidc":true,"addedLater":{"x":1}}"""
        val status = ServerChecker(engine(body = body, seen = seen)).check(url)
        assertEquals(ServerStatus.Ready(url, "0.3.0", oidc = true), status)
        assertEquals(listOf("https://chores.example.com/sub/api/v1/version"), seen)
    }

    @Test
    fun incompatible() = runTest {
        val body = """{"version":"2.0.0","commit":"abc","apiVersion":2,"oidc":false}"""
        val status = ServerChecker(engine(body = body)).check(url)
        assertEquals(ServerStatus.Incompatible(url, Compatibility.AppTooOld(2), "2.0.0"), status)
    }

    @Test
    fun notChorecast() = runTest {
        val html = headersOf(HttpHeaders.ContentType, ContentType.Text.Html.toString())
        for (e in listOf(
            engine(HttpStatusCode.NotFound, "<html>nope</html>", html),
            engine(body = "<html>router login</html>", headers = html),
            engine(body = """{"something":"else"}"""),
        )) {
            assertEquals(ServerStatus.NotChorecast(url), ServerChecker(e).check(url))
        }
    }

    @Test
    fun unreachable() = runTest {
        val status = ServerChecker(MockEngine { throw IOException("connection refused") }).check(url)
        assertTrue(status is ServerStatus.Unreachable)
        assertEquals("connection refused", (status as ServerStatus.Unreachable).reason)
    }
}
