package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.InboxApi
import dev.voidrot.chorecast.api.infrastructure.HttpResponse
import dev.voidrot.chorecast.api.models.InboxItem
import dev.voidrot.chorecast.api.models.InboxPage
import io.ktor.client.engine.HttpClientEngine
import kotlinx.coroutines.CancellationException
import java.io.IOException
import java.time.OffsetDateTime

/** The quick-pick reasons a parent can reject with. Unknown codes map to null and show the text only. */
enum class RejectionReason(val code: String) {
    NOT_DONE("not_done"),
    NOT_FINISHED("not_finished"),
    NEEDS_REDO("needs_redo"),
    EVIDENCE_UNCLEAR("evidence_unclear"),
    ;

    companion object {
        fun fromCode(code: String?): RejectionReason? = entries.firstOrNull { it.code == code }
    }
}

/**
 * What an inbox item says, as parameters. The server stores no text, so the app renders each case
 * from its strings. [Other] covers kinds this app version doesn't know.
 */
sealed interface InboxMessage {
    data class Submitted(val choreName: String) : InboxMessage
    data class Approved(val choreName: String, val auto: Boolean) : InboxMessage
    data class Rejected(val choreName: String, val reason: RejectionReason?, val text: String?) : InboxMessage
    data class Missed(val choreName: String) : InboxMessage
    data class Excused(val choreName: String) : InboxMessage
    data class Streak(val days: Long, val points: Long) : InboxMessage
    data object Other : InboxMessage
}

/** One inbox row. [instanceId] is the chore instance it is about, when it is about one. */
data class InboxEntry(
    val id: Long,
    val message: InboxMessage,
    val createdAt: OffsetDateTime,
    val read: Boolean,
    val kidId: Long?,
    val instanceId: Long?,
)

/** Maps a server item to a display model. Every `when` has an `else` so a newer server never breaks the list. */
fun InboxItem.toEntry(): InboxEntry {
    val name = data.choreName.orEmpty()
    val message = when (kind) {
        InboxItem.Kind.CHORE_SUBMITTED -> InboxMessage.Submitted(name)
        InboxItem.Kind.CHORE_APPROVED -> InboxMessage.Approved(name, data.auto == true)
        InboxItem.Kind.CHORE_REJECTED ->
            InboxMessage.Rejected(name, RejectionReason.fromCode(data.reasonCode), data.reasonText?.takeIf { it.isNotBlank() })
        InboxItem.Kind.CHORE_MISSED -> InboxMessage.Missed(name)
        InboxItem.Kind.CHORE_EXCUSED -> InboxMessage.Excused(name)
        InboxItem.Kind.STREAK_MILESTONE -> InboxMessage.Streak(data.days ?: 0, data.points ?: 0)
        else -> InboxMessage.Other
    }
    return InboxEntry(
        id = id,
        message = message,
        createdAt = createdAt,
        read = readAt != null,
        kidId = kidId,
        instanceId = refId.takeIf { refKind == InboxItem.RefKind.INSTANCE },
    )
}

/**
 * The inbox as loaded so far: the pages merged, the cursor for the next, and the unread count
 * (which covers all notifications, not just those loaded).
 */
data class InboxFeed(
    val entries: List<InboxEntry> = emptyList(),
    val nextCursor: String? = null,
    val unread: Long = 0,
) {
    /** Adds the next page after the loaded ones, skipping ids already present (a page can overlap after new arrivals). */
    fun append(page: InboxFeed): InboxFeed {
        val have = entries.mapTo(HashSet()) { it.id }
        return InboxFeed(entries + page.entries.filter { it.id !in have }, page.nextCursor, page.unread)
    }

    /** Marks one entry read here; the count drops only if it was unread. */
    fun markRead(id: Long): InboxFeed {
        val target = entries.firstOrNull { it.id == id }
        if (target == null || target.read) return this
        return copy(
            entries = entries.map { if (it.id == id) it.copy(read = true) else it },
            unread = (unread - 1).coerceAtLeast(0),
        )
    }

    fun markAllRead(): InboxFeed = copy(entries = entries.map { it.copy(read = true) }, unread = 0)
}

/** Reads the inbox of whoever the token belongs to: a parent, a kid's device or a tablet kid session. */
class InboxClient(private val engine: HttpClientEngine? = null) {
    private fun api(url: ServerUrl, token: String) =
        InboxApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    /** One page, newest first; pass the previous page's cursor for the next. */
    suspend fun list(url: ServerUrl, token: String, cursor: String? = null, limit: Long? = null): Fetch<InboxFeed> = guard {
        val resp = if (limit == null) api(url, token).listInbox(cursor = cursor) else api(url, token).listInbox(cursor = cursor, limit = limit)
        if (resp.success) Fetch.Ok(resp.body().toFeed()) else resp.failure()
    }

    suspend fun markRead(url: ServerUrl, token: String, id: Long): Fetch<Unit> = guard {
        val resp = api(url, token).markInboxRead(id)
        if (resp.success) Fetch.Ok(Unit) else resp.failure()
    }

    suspend fun markAllRead(url: ServerUrl, token: String): Fetch<Unit> = guard {
        val resp = api(url, token).markAllInboxRead()
        if (resp.success) Fetch.Ok(Unit) else resp.failure()
    }

    private fun InboxPage.toFeed() = InboxFeed(items.map(InboxItem::toEntry), nextCursor, unreadCount)

    private fun HttpResponse<*>.failure(): Fetch<Nothing> = when (status) {
        401 -> Fetch.Unauthorized
        404 -> Fetch.NotFound
        else -> Fetch.Failed("http_$status")
    }

    private suspend fun <T> guard(block: suspend () -> Fetch<T>): Fetch<T> = try {
        block()
    } catch (e: CancellationException) {
        throw e
    } catch (e: IOException) {
        Fetch.Unreachable(e.message ?: e::class.simpleName.orEmpty())
    } catch (e: Exception) {
        Fetch.Failed(e.message ?: e::class.simpleName.orEmpty())
    }
}
