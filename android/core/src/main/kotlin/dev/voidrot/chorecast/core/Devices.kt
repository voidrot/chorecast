package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.DevicesApi
import dev.voidrot.chorecast.api.apis.KidsApi
import dev.voidrot.chorecast.api.infrastructure.HttpResponse
import dev.voidrot.chorecast.api.models.Device
import dev.voidrot.chorecast.api.models.DevicePicker
import dev.voidrot.chorecast.api.models.CreatePairingInputBody
import dev.voidrot.chorecast.api.models.ElevateInputBody
import dev.voidrot.chorecast.api.models.Kid
import dev.voidrot.chorecast.api.models.KidSessionInputBody
import dev.voidrot.chorecast.api.models.ListedDevice
import dev.voidrot.chorecast.api.models.PairDeviceInputBody
import dev.voidrot.chorecast.api.models.Problem
import io.ktor.client.engine.HttpClientEngine
import io.ktor.util.reflect.typeInfo
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.jsonPrimitive
import java.io.IOException
import java.time.Instant

/** What a pairing QR code holds: `{"server": "...", "code": "..."}`. */
data class PairingQr(val server: ServerUrl, val code: String) {
    companion object {
        /** Parses a scanned QR payload; null if it isn't a Chorecast pairing code. */
        fun parse(payload: String): PairingQr? {
            val obj = try {
                ApiClients.json.parseToJsonElement(payload.trim()).jsonObject
            } catch (_: Exception) {
                return null
            }
            val server = obj["server"]?.jsonPrimitive?.content ?: return null
            val code = obj["code"]?.jsonPrimitive?.content?.takeIf { it.isNotBlank() } ?: return null
            val url = (ServerUrl.parse(server) as? Result.Ok)?.value ?: return null
            return PairingQr(url, code)
        }
    }

    /** The payload [parse] reads back; this is what the pairing QR code encodes. */
    fun toPayload(): String = buildJsonObject {
        put("server", server.value)
        put("code", code)
    }.toString()
}

/** A pairing code a parent created: show [qr] and the typed [code] until [expiresAt]. */
data class IssuedPairing(val qr: PairingQr, val expiresAt: Instant, val kidId: Long?) {
    val code: String get() = qr.code
}

sealed interface CreatePairingResult {
    data class Created(val pairing: IssuedPairing) : CreatePairingResult

    /** The parent's token stopped working. */
    data object SignedOut : CreatePairingResult
    data class Unreachable(val reason: String) : CreatePairingResult
    data class Failed(val code: String) : CreatePairingResult
}

sealed interface PairResult {
    data class Paired(val token: String, val device: Device) : PairResult
    data object InvalidCode : PairResult
    data object RateLimited : PairResult
    data class Unreachable(val reason: String) : PairResult
    data class Failed(val code: String) : PairResult
}

/** A short-lived kid session or parent elevation on this device. */
data class DeviceSessionToken(val token: String, val userId: Long, val expiresAt: Instant)

sealed interface SessionResult {
    data class Started(val session: DeviceSessionToken) : SessionResult
    data object WrongPin : SessionResult
    data class Locked(val retryAfterSeconds: Long?) : SessionResult

    /** The device token no longer works (revoked or the kid was archived): pair again. */
    data object Unpaired : SessionResult
    data class Unreachable(val reason: String) : SessionResult
    data class Failed(val code: String) : SessionResult
}

/** The device side of pairing: pair, read the profile picker, start sessions, elevate, lock. */
class DeviceClient(private val engine: HttpClientEngine? = null) {
    private fun api(url: ServerUrl, token: String? = null) =
        DevicesApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config).apply {
            token?.let { setBearerToken(it) }
        }

    suspend fun pair(url: ServerUrl, code: String, deviceName: String, appVersion: String): PairResult = guard(
        { PairResult.Unreachable(it) },
        { PairResult.Failed("unexpected") },
    ) {
        val resp = api(url).pairDevice(PairDeviceInputBody(code = code.trim(), name = deviceName.take(60), appVersion = appVersion.take(30)))
        if (resp.success) {
            val body = resp.body()
            return@guard PairResult.Paired(body.token, body.device)
        }
        when (val c = resp.problemCode()) {
            "invalid_pairing_code" -> PairResult.InvalidCode
            "rate_limited" -> PairResult.RateLimited
            else -> PairResult.Failed(c)
        }
    }

    /**
     * The picker, or null if the device token stopped working. Throws IOException when offline.
     * [appVersion] is recorded for the parents' devices page.
     */
    suspend fun picker(url: ServerUrl, deviceToken: String, appVersion: String? = null): DevicePicker? {
        val resp = api(url, deviceToken).getDevicePicker(appVersion)
        return when {
            resp.success -> resp.body()
            resp.status == 401 || resp.status == 403 -> null
            else -> throw IOException("HTTP ${resp.status}")
        }
    }

    suspend fun startKidSession(url: ServerUrl, deviceToken: String, kidId: Long, pin: String?): SessionResult = session {
        api(url, deviceToken).startKidSession(KidSessionInputBody(kid = kidId.toString(), pin = pin?.takeIf { it.isNotEmpty() }))
    }

    suspend fun elevate(url: ServerUrl, deviceToken: String, parentId: Long, pin: String): SessionResult = session {
        api(url, deviceToken).elevateParent(ElevateInputBody(parentId = parentId, pin = pin))
    }

    /** Ends a kid session or elevation on the server. Best effort: the app forgets it either way. */
    suspend fun lock(url: ServerUrl, sessionToken: String) {
        guard({}, {}) { api(url, sessionToken).lockDevice() }
    }

    // Parents managing devices (app token).

    /** All paired devices, or null if the parent's token stopped working. Throws IOException when offline. */
    suspend fun list(url: ServerUrl, appToken: String): List<ListedDevice>? {
        val resp = api(url, appToken).listDevices()
        return when {
            resp.success -> resp.body().items
            resp.status == 401 || resp.status == 403 -> null
            else -> throw IOException("HTTP ${resp.status}")
        }
    }

    /** Sends a shared device back to its picker. True when the server accepted it. */
    suspend fun lockRemotely(url: ServerUrl, appToken: String, deviceId: Long): Boolean =
        guard({ false }, { false }) { api(url, appToken).lockDeviceRemotely(deviceId).success }

    /** Unpairs a device. True when it's gone (including already gone). */
    suspend fun revoke(url: ServerUrl, appToken: String, deviceId: Long): Boolean = guard({ false }, { false }) {
        val resp = api(url, appToken).revokeDevice(deviceId)
        resp.success || resp.status == 404
    }

    /** The active kids a device can be paired to, or null if the parent's token stopped working. Throws IOException when offline. */
    suspend fun kids(url: ServerUrl, appToken: String): List<Kid>? {
        val resp = try {
            KidsApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
                .apply { setBearerToken(appToken) }
                .listKids()
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            throw e
        } catch (e: Exception) {
            throw IOException(e.message ?: e::class.simpleName.orEmpty(), e) // e.g. an unreadable body
        }
        return when {
            resp.success -> try {
                resp.body().items
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                throw IOException(e.message ?: e::class.simpleName.orEmpty(), e)
            }
            resp.status == 401 || resp.status == 403 -> null
            else -> throw IOException("HTTP ${resp.status}")
        }
    }

    /**
     * Creates a single-use pairing code for [kidId]'s own device, or for a shared family device when
     * null. No Idempotency-Key: every call is meant to make a new code.
     */
    suspend fun createPairing(url: ServerUrl, appToken: String, kidId: Long?): CreatePairingResult = guard(
        { CreatePairingResult.Unreachable(it) },
        { CreatePairingResult.Failed("unexpected") },
    ) {
        val resp = api(url, appToken).createPairing(CreatePairingInputBody(kid = kidId?.toString()))
        if (resp.success) {
            val b = resp.body()
            // The code is for a phone that has to reach the server, so use the address the server gave.
            val server = (ServerUrl.parse(b.server) as? Result.Ok)?.value
                ?: return@guard CreatePairingResult.Failed("bad_server_url")
            return@guard CreatePairingResult.Created(IssuedPairing(PairingQr(server, b.code), b.expiresAt.toInstant(), b.kidId))
        }
        if (resp.status == 401 || resp.status == 403) CreatePairingResult.SignedOut else CreatePairingResult.Failed(resp.problemCode())
    }

    private suspend fun session(call: suspend () -> HttpResponse<dev.voidrot.chorecast.api.models.DeviceSession>): SessionResult =
        guard({ SessionResult.Unreachable(it) }, { SessionResult.Failed("unexpected") }) {
            val resp = call()
            if (resp.success) {
                val b = resp.body()
                return@guard SessionResult.Started(DeviceSessionToken(b.token, b.userId, b.expiresAt.toInstant()))
            }
            if (resp.status == 401) return@guard SessionResult.Unpaired
            when (val c = resp.problemCode()) {
                "invalid_pin" -> SessionResult.WrongPin
                "pin_locked" -> SessionResult.Locked(resp.retryAfter())
                else -> SessionResult.Failed(c)
            }
        }

    private fun HttpResponse<*>.retryAfter(): Long? =
        headers.entries.firstOrNull { it.key.equals("Retry-After", ignoreCase = true) }?.value?.firstOrNull()?.toLongOrNull()

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
