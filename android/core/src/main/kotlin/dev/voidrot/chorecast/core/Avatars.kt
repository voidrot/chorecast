package dev.voidrot.chorecast.core

import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.engine.HttpClientEngine
import io.ktor.client.request.get
import io.ktor.http.isSuccess
import kotlinx.coroutines.CancellationException

/** How many bundled avatars the app ships (`pixel-01` to `pixel-24`, as on the server). */
const val BUNDLED_AVATAR_COUNT = 24

private val bundledAvatarId = Regex("pixel-(\\d{2})")

/** The 1-based number of a bundled avatar id such as `pixel-07`, or null for an id this app doesn't ship. */
fun bundledAvatarNumber(id: String?): Int? {
    val n = bundledAvatarId.matchEntire(id.orEmpty())?.groupValues?.get(1)?.toInt() ?: return null
    return n.takeIf { it in 1..BUNDLED_AVATAR_COUNT }
}

/**
 * Downloads a kid's photo avatar from the signed link in the picker. Signed links need no token and
 * expire, so a failure just means the picker falls back to the bundled avatar or initials.
 */
class AvatarPhotos(engine: HttpClientEngine) {
    private val http = HttpClient(engine)

    /** The image bytes, or null if the link is refused, expired, too large or unreachable. */
    suspend fun fetch(url: String): ByteArray? = try {
        val resp = http.get(url)
        val length = resp.headers["Content-Length"]?.toLongOrNull()
        if (!resp.status.isSuccess() || (length != null && length > MAX_BYTES)) {
            null
        } else {
            resp.body<ByteArray>().takeIf { it.size <= MAX_BYTES }
        }
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        null
    }

    companion object {
        /** Thumbnails are small; anything larger is not an avatar. */
        const val MAX_BYTES = 2_000_000L
    }
}
