package dev.voidrot.chorecast.core

import java.net.URI
import java.net.URLEncoder
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.Base64

/** PKCE (RFC 7636) for the app's single sign-on: the server only ever sees the challenge first. */
data class Pkce(val verifier: String, val challenge: String) {
    companion object {
        private val random = SecureRandom()
        private val b64 = Base64.getUrlEncoder().withoutPadding()

        fun generate(): Pkce {
            val bytes = ByteArray(32).also(random::nextBytes)
            return of(b64.encodeToString(bytes))
        }

        fun of(verifier: String): Pkce {
            val digest = MessageDigest.getInstance("SHA-256").digest(verifier.toByteArray(Charsets.US_ASCII))
            return Pkce(verifier, b64.encodeToString(digest))
        }
    }
}

/** What came back on the app's `chorecast://auth` redirect. */
sealed interface SsoCallback {
    data class Code(val code: String) : SsoCallback

    /** [error] is the server's code: not_linked, denied, expired, failed... */
    data class Error(val error: String) : SsoCallback

    companion object {
        const val REDIRECT = "chorecast://auth"

        /** Parses a redirect URI; null if it isn't ours. */
        fun parse(uri: String): SsoCallback? {
            val u = try {
                URI(uri)
            } catch (_: Exception) {
                return null
            }
            if (u.scheme != "chorecast" || u.host != "auth") return null
            val params = u.rawQuery.orEmpty().split('&').filter { it.isNotEmpty() }.associate {
                val (k, v) = (it.split('=', limit = 2) + "").take(2)
                k to java.net.URLDecoder.decode(v, Charsets.UTF_8)
            }
            params["code"]?.takeIf { it.isNotEmpty() }?.let { return Code(it) }
            return Error(params["error"]?.takeIf { it.isNotEmpty() } ?: "failed")
        }
    }
}

/** The browser URL that starts single sign-on for the app. */
fun ssoStartUrl(server: ServerUrl, pkce: Pkce, deviceLabel: String): String {
    fun enc(s: String) = URLEncoder.encode(s, Charsets.UTF_8)
    return "${server.apiBase}/auth/oidc/start?client=app&challenge=${enc(pkce.challenge)}&label=${enc(deviceLabel.take(100))}"
}
