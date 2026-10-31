package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.SystemApi
import io.ktor.client.HttpClientConfig
import io.ktor.client.engine.HttpClientEngine
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.serialization.kotlinx.json.json
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.Json
import java.io.IOException

/** Shared configuration for every generated API client. */
object ApiClients {
    /** Unknown fields are ignored: the server only adds fields within an API major. */
    val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    val config: (HttpClientConfig<*>) -> Unit = {
        it.install(ContentNegotiation) { json(json) }
        it.expectSuccess = false
    }
}

/** The outcome of checking a server before connecting to it. */
sealed interface ServerStatus {
    data class Ready(val url: ServerUrl, val serverVersion: String, val oidc: Boolean) : ServerStatus
    data class Incompatible(val url: ServerUrl, val compatibility: Compatibility, val serverVersion: String) : ServerStatus

    /** Nothing answered (DNS, connection refused, timeout, TLS). */
    data class Unreachable(val url: ServerUrl, val reason: String) : ServerStatus

    /** Something answered, but not a Chorecast server (or not this API major). */
    data class NotChorecast(val url: ServerUrl) : ServerStatus
}

/** Checks a server's identity and API compatibility via `GET /api/v1/version`. */
class ServerChecker(private val engine: HttpClientEngine? = null) {
    suspend fun check(url: ServerUrl): ServerStatus {
        val api = SystemApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
        return try {
            val resp = api.getVersion()
            if (!resp.success) return ServerStatus.NotChorecast(url)
            val body = resp.body()
            when (val c = Compatibility.of(body.apiVersion)) {
                Compatibility.Compatible -> ServerStatus.Ready(url, body.version, body.oidc)
                else -> ServerStatus.Incompatible(url, c, body.version)
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            ServerStatus.Unreachable(url, e.message ?: e::class.simpleName.orEmpty())
        } catch (_: Exception) {
            // Answered, but not with a Chorecast version document (HTML page, wrong JSON, ...).
            ServerStatus.NotChorecast(url)
        }
    }
}
