package dev.voidrot.chorecast.core

import java.net.URI
import java.net.URISyntaxException

/**
 * A validated Chorecast server base URL, e.g. `https://chores.example.com` or
 * `http://192.168.1.20:8080`. Normally it comes from the pairing QR code; [parse] also accepts
 * what a person types.
 */
@JvmInline
value class ServerUrl private constructor(val value: String) {
    /** Base URL of the v1 API. */
    val apiBase: String get() = "$value/api/v1"

    /** True for plain-HTTP servers; the app warns because tokens travel unencrypted. */
    val isCleartext: Boolean get() = value.startsWith("http://")

    override fun toString(): String = value

    sealed interface Error {
        data object Empty : Error
        data object UnsupportedScheme : Error
        data object Invalid : Error
    }

    companion object {
        /**
         * Normalizes and validates user input: trims, defaults to https when no scheme is given,
         * drops trailing slashes and a pasted `/api/v1` suffix, and rejects anything but http(s)
         * URLs with a host and no credentials, query or fragment. Paths are kept for servers behind
         * a reverse proxy on a sub-path.
         */
        fun parse(input: String): Result<ServerUrl, Error> {
            var s = input.trim()
            if (s.isEmpty()) return Result.Err(Error.Empty)
            if (!s.contains("://")) s = "https://$s"
            val uri = try {
                URI(s)
            } catch (_: URISyntaxException) {
                return Result.Err(Error.Invalid)
            }
            val scheme = uri.scheme?.lowercase() ?: return Result.Err(Error.Invalid)
            if (scheme != "http" && scheme != "https") return Result.Err(Error.UnsupportedScheme)
            val host = uri.host
            if (host.isNullOrEmpty() || uri.rawUserInfo != null || uri.rawQuery != null || uri.rawFragment != null) {
                return Result.Err(Error.Invalid)
            }
            val path = (uri.rawPath ?: "").trimEnd('/').removeSuffix("/api/v1").trimEnd('/')
            val port = if (uri.port == -1) "" else ":${uri.port}"
            return Result.Ok(ServerUrl("$scheme://${host.lowercase()}$port$path"))
        }
    }
}

/** A minimal result type that keeps typed errors (kotlin.Result only carries Throwables). */
sealed interface Result<out T, out E> {
    data class Ok<T>(val value: T) : Result<T, Nothing>
    data class Err<E>(val error: E) : Result<Nothing, E>
}
