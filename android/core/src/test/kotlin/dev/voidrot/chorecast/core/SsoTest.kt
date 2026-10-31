package dev.voidrot.chorecast.core

import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.ContentType
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.content.OutgoingContent
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class SsoTest {
    private val url = (ServerUrl.parse("https://chores.example.com/sub") as Result.Ok).value

    @Test
    fun pkceMatchesRfc7636Example() {
        // RFC 7636 appendix B.
        val p = Pkce.of("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
        assertEquals("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", p.challenge)
        val a = Pkce.generate()
        assertEquals(43, a.verifier.length)
        assertEquals(43, a.challenge.length)
        assertNotEquals(a, Pkce.generate())
    }

    @Test
    fun startUrl() {
        val p = Pkce.of("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
        assertEquals(
            "https://chores.example.com/sub/api/v1/auth/oidc/start?client=app" +
                "&challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM&label=Google+Pixel+9",
            ssoStartUrl(url, p, "Google Pixel 9"),
        )
    }

    @Test
    fun parsesCallbacks() {
        assertEquals(SsoCallback.Code("a-b_c"), SsoCallback.parse("chorecast://auth?code=a-b_c"))
        assertEquals(SsoCallback.Error("not_linked"), SsoCallback.parse("chorecast://auth?error=not_linked"))
        assertEquals(SsoCallback.Error("failed"), SsoCallback.parse("chorecast://auth"))
        assertNull(SsoCallback.parse("https://evil.example/auth?code=x"))
        assertNull(SsoCallback.parse("chorecast://other?code=x"))
    }

    @Test
    fun exchangeSendsCodeAndVerifier() = runTest {
        var sent = ""
        val engine = MockEngine { req ->
            sent = (req.body as OutgoingContent.ByteArrayContent).bytes().decodeToString()
            assertEquals("https://chores.example.com/sub/api/v1/auth/oidc/exchange", req.url.toString())
            respond("""{"userId":3,"token":"tok"}""", HttpStatusCode.OK, headersOf(HttpHeaders.ContentType, ContentType.Application.Json.toString()))
        }
        val p = Pkce.generate()
        assertEquals(LoginResult.Success("tok", 3, null), ParentAuth(engine).exchangeSso(url, "code1", p, "Phone"))
        assertTrue(sent.contains("\"code\":\"code1\"") && sent.contains("\"verifier\":\"${p.verifier}\""))

        val bad = MockEngine {
            respond("""{"status":400,"code":"oidc_invalid_code"}""", HttpStatusCode.BadRequest, headersOf(HttpHeaders.ContentType, "application/problem+json"))
        }
        assertEquals(LoginResult.Failed("oidc_invalid_code"), ParentAuth(bad).exchangeSso(url, "x", p, "Phone"))
    }
}
