package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.SystemApi
import io.ktor.client.HttpClientConfig
import io.ktor.client.engine.HttpClientEngine
import io.ktor.client.plugins.contentnegotiation.ContentNegotiation
import io.ktor.serialization.kotlinx.json.json
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.KSerializer
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.Json
import kotlinx.serialization.modules.SerializersModule
import java.io.IOException
import java.time.LocalDate
import java.time.OffsetDateTime

/** Shared configuration for every generated API client. */
object ApiClients {
    /**
     * Unknown fields are ignored: the server only adds fields within an API major. The generated models
     * mark date-times and dates `@Contextual`; their serializers are registered here.
     */
    val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        serializersModule = SerializersModule {
            contextual(OffsetDateTime::class, OffsetDateTimeSerializer)
            contextual(LocalDate::class, LocalDateSerializer)
        }
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

/** RFC 3339 date-times, as the server sends them. */
object OffsetDateTimeSerializer : KSerializer<OffsetDateTime> {
    override val descriptor = PrimitiveSerialDescriptor("OffsetDateTime", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: OffsetDateTime) = encoder.encodeString(value.toString())
    override fun deserialize(decoder: Decoder): OffsetDateTime = OffsetDateTime.parse(decoder.decodeString())
}

/** YYYY-MM-DD civil dates. */
object LocalDateSerializer : KSerializer<LocalDate> {
    override val descriptor = PrimitiveSerialDescriptor("LocalDate", PrimitiveKind.STRING)
    override fun serialize(encoder: Encoder, value: LocalDate) = encoder.encodeString(value.toString())
    override fun deserialize(decoder: Decoder): LocalDate = LocalDate.parse(decoder.decodeString())
}
