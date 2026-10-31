package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.AuthApi
import dev.voidrot.chorecast.api.infrastructure.HttpResponse
import dev.voidrot.chorecast.api.models.ExchangeOIDCInputBody
import dev.voidrot.chorecast.api.models.LoginInputBody
import dev.voidrot.chorecast.api.models.MeOutputBody
import dev.voidrot.chorecast.api.models.Problem
import io.ktor.client.engine.HttpClientEngine
import io.ktor.util.reflect.typeInfo
import kotlinx.coroutines.CancellationException
import java.io.IOException

/** The second sign-in step when two-factor sign-in is on. */
sealed interface SecondFactor {
    data class Code(val code: String) : SecondFactor
    data class Recovery(val code: String) : SecondFactor
}

sealed interface LoginResult {
    data class Success(val token: String, val userId: Long, val recoveryCodesLeft: Long?) : LoginResult

    /** Password was right; ask for an authenticator or recovery code and send it again. */
    data object SecondFactorRequired : LoginResult
    data object InvalidCredentials : LoginResult
    data object InvalidCode : LoginResult
    data class RateLimited(val retryAfterSeconds: Long?) : LoginResult
    data class Unreachable(val reason: String) : LoginResult

    /** Anything else; [code] is the server's error code (or "unexpected"). */
    data class Failed(val code: String) : LoginResult
}

sealed interface SessionCheck {
    data class SignedIn(val me: MeOutputBody) : SessionCheck

    /** The token was revoked or expired: sign in again. */
    data object SignedOut : SessionCheck
    data class Unreachable(val reason: String) : SessionCheck
}

/** Parent sign-in for the app: `POST /auth/login` with `client: app` returns a bearer token. */
class ParentAuth(private val engine: HttpClientEngine? = null) {
    private fun api(url: ServerUrl, token: String? = null) =
        AuthApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config).apply {
            token?.let { setBearerToken(it) }
        }

    suspend fun login(
        url: ServerUrl,
        username: String,
        password: String,
        deviceLabel: String,
        secondFactor: SecondFactor? = null,
    ): LoginResult = guard({ LoginResult.Unreachable(it) }, { LoginResult.Failed("unexpected") }) {
        val resp = api(url).login(
            LoginInputBody(
                client = LoginInputBody.Client.APP,
                username = username.trim(),
                password = password,
                label = deviceLabel.take(100),
                totpCode = (secondFactor as? SecondFactor.Code)?.code?.trim(),
                recoveryCode = (secondFactor as? SecondFactor.Recovery)?.code?.trim(),
            ),
        )
        if (resp.success) {
            val body = resp.body()
            val token = body.token ?: return@guard LoginResult.Failed("no_token")
            return@guard LoginResult.Success(token, body.userId, body.recoveryCodesLeft)
        }
        when (val code = resp.problemCode()) {
            "totp_required" -> LoginResult.SecondFactorRequired
            "invalid_credentials" -> LoginResult.InvalidCredentials
            "invalid_totp_code" -> LoginResult.InvalidCode
            "rate_limited" -> LoginResult.RateLimited(resp.headers.entries
                .firstOrNull { it.key.equals("Retry-After", ignoreCase = true) }?.value?.firstOrNull()?.toLongOrNull())
            else -> LoginResult.Failed(code)
        }
    }

    /** Exchanges the one-time code from the SSO redirect, with the verifier that started it, for a token. */
    suspend fun exchangeSso(url: ServerUrl, code: String, pkce: Pkce, deviceLabel: String): LoginResult =
        guard({ LoginResult.Unreachable(it) }, { LoginResult.Failed("unexpected") }) {
            val resp = api(url).exchangeOidcCode(
                ExchangeOIDCInputBody(code = code, verifier = pkce.verifier, label = deviceLabel.take(100)),
            )
            if (resp.success) {
                val body = resp.body()
                LoginResult.Success(body.token, body.userId, null)
            } else {
                LoginResult.Failed(resp.problemCode())
            }
        }

    /** Checks a stored token: who it belongs to, or that it no longer works. */
    suspend fun check(url: ServerUrl, token: String): SessionCheck =
        guard({ SessionCheck.Unreachable(it) }, { SessionCheck.Unreachable(it) }) {
            val resp = api(url, token).getMe()
            when {
                resp.success -> SessionCheck.SignedIn(resp.body())
                resp.status == 401 || resp.status == 403 -> SessionCheck.SignedOut
                else -> SessionCheck.Unreachable("HTTP ${resp.status}")
            }
        }

    /** Revokes the token on the server. Best effort: the app forgets it either way. */
    suspend fun logout(url: ServerUrl, token: String) {
        guard({}, {}) { api(url, token).logout() }
    }

    private suspend fun HttpResponse<*>.problemCode(): String = try {
        typedBody<Problem>(typeInfo<Problem>()).code
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        "http_$status"
    }

    private inline fun <T> guard(unreachable: (String) -> T, other: (String) -> T, block: () -> T): T = try {
        block()
    } catch (e: CancellationException) {
        throw e
    } catch (e: IOException) {
        unreachable(e.message ?: e::class.simpleName.orEmpty())
    } catch (e: Exception) {
        other(e.message ?: e::class.simpleName.orEmpty())
    }
}
