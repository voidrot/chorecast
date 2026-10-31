package dev.voidrot.chorecast.core

import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.client.request.HttpRequestData
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.content.OutgoingContent
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

class ParentAuthTest {
    private val url = (ServerUrl.parse("https://chores.example.com") as Result.Ok).value
    private val json = headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString())

    private fun problem(status: HttpStatusCode, code: String, extra: Map<String, String> = emptyMap()) =
        MockEngine {
            val headers = headersOf(
                *(listOf(HttpHeaders.ContentType to listOf("application/problem+json")) +
                    extra.map { (k, v) -> k to listOf(v) }).toTypedArray(),
            )
            respond("""{"status":${status.value},"code":"$code","detail":"x"}""", status, headers)
        }

    private fun bodyOf(req: HttpRequestData): String =
        (req.body as OutgoingContent.ByteArrayContent).bytes().decodeToString()

    @Test
    fun successSendsAppClientAndSecondFactor() = runTest {
        val seen = mutableListOf<HttpRequestData>()
        val engine = MockEngine { req ->
            seen += req
            respond("""{"userId":7,"token":"tok","recoveryCodesLeft":3}""", HttpStatusCode.OK, json)
        }
        val res = ParentAuth(engine).login(url, " mom ", "pw", "Pixel 9", SecondFactor.Recovery("abcd-efgh-jkmn"))
        assertEquals(LoginResult.Success("tok", 7, 3), res)
        val req = seen.single()
        assertEquals("https://chores.example.com/api/v1/auth/login", req.url.toString())
        val body = bodyOf(req)
        for (part in listOf("\"client\":\"app\"", "\"username\":\"mom\"", "\"label\":\"Pixel 9\"", "\"recoveryCode\":\"abcd-efgh-jkmn\"")) {
            assertTrue("$part missing from $body", body.contains(part))
        }
        assertTrue(!body.contains("totpCode"))
    }

    @Test
    fun mapsErrorCodes() = runTest {
        val auth = { e: MockEngine -> ParentAuth(e) }
        assertEquals(LoginResult.SecondFactorRequired, auth(problem(HttpStatusCode.Unauthorized, "totp_required")).login(url, "mom", "pw", "x"))
        assertEquals(LoginResult.InvalidCredentials, auth(problem(HttpStatusCode.Unauthorized, "invalid_credentials")).login(url, "mom", "pw", "x"))
        assertEquals(LoginResult.InvalidCode, auth(problem(HttpStatusCode.Unauthorized, "invalid_totp_code")).login(url, "mom", "pw", "x", SecondFactor.Code("123456")))
        assertEquals(
            LoginResult.RateLimited(900),
            auth(problem(HttpStatusCode.TooManyRequests, "rate_limited", mapOf("Retry-After" to "900"))).login(url, "mom", "pw", "x"),
        )
        assertEquals(LoginResult.Failed("internal_error"), auth(problem(HttpStatusCode.InternalServerError, "internal_error")).login(url, "mom", "pw", "x"))
    }

    @Test
    fun unreachable() = runTest {
        val res = ParentAuth(MockEngine { throw IOException("connection refused") }).login(url, "mom", "pw", "x")
        assertEquals(LoginResult.Unreachable("connection refused"), res)
    }

    @Test
    fun checkSendsBearerToken() = runTest {
        val seen = mutableListOf<HttpRequestData>()
        val ok = MockEngine { req ->
            seen += req
            respond("""{"kind":"parent","userId":7,"owner":true,"scopes":[],"totpEnabled":false,"pinSet":false,"oidcLinked":false,"displayName":"Mom"}""", HttpStatusCode.OK, json)
        }
        val res = ParentAuth(ok).check(url, "tok")
        assertTrue(res is SessionCheck.SignedIn && res.me.displayName == "Mom")
        assertEquals("Bearer tok", seen.single().headers[HttpHeaders.Authorization])

        assertEquals(SessionCheck.SignedOut, ParentAuth(problem(HttpStatusCode.Unauthorized, "unauthenticated")).check(url, "tok"))
    }
}
