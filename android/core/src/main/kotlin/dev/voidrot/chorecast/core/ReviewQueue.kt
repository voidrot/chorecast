package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.EvidencePhoto
import dev.voidrot.chorecast.api.models.RejectChoreInstanceInputBody
import dev.voidrot.chorecast.api.models.ReviewQueueApprovalResult
import dev.voidrot.chorecast.api.models.ReviewQueueItem
import java.time.Duration
import java.time.Instant
import java.util.UUID

/** The server accepts at most this many ids in one bulk approval. */
const val BULK_APPROVE_MAX = 100

/** The server's limit on a rejection's free text. */
const val REJECT_TEXT_MAX = 500

/** Signed photo links last about 10 minutes, so a loaded queue is reloaded before they lapse. */
val QUEUE_STALE_AFTER: Duration = Duration.ofMinutes(8)

/** The quick-pick rejection reasons, in the order the web offers them. */
val REJECT_REASONS: List<RejectChoreInstanceInputBody.Reason> = listOf(
    RejectChoreInstanceInputBody.Reason.NOT_DONE,
    RejectChoreInstanceInputBody.Reason.NOT_FINISHED,
    RejectChoreInstanceInputBody.Reason.NEEDS_REDO,
    RejectChoreInstanceInputBody.Reason.EVIDENCE_UNCLEAR,
)

/** One kid's waiting submissions, oldest first as the API lists them. */
data class ReviewGroup(val key: String, val kidName: String, val items: List<ReviewQueueItem>)

/**
 * Groups the flat, oldest-first queue by kid. Groups appear in the order their oldest item does.
 * The key is the completer (else the assignee); items with neither group by the kid's name.
 */
fun groupReviewByKid(items: List<ReviewQueueItem>): List<ReviewGroup> {
    val groups = LinkedHashMap<String, MutableList<ReviewQueueItem>>()
    val names = HashMap<String, String>()
    for (item in items) {
        val key = reviewKidKey(item)
        groups.getOrPut(key) { mutableListOf() }.add(item)
        names.putIfAbsent(key, item.kidName)
    }
    return groups.map { (key, list) -> ReviewGroup(key, names.getValue(key), list) }
}

private fun reviewKidKey(item: ReviewQueueItem): String =
    (item.instance.completedBy ?: item.instance.kidId)?.let { "kid:$it" } ?: "name:${item.kidName}"

/** The kid id behind a queue item, as [groupReviewByKid] keys it. */
fun ReviewQueueItem.kidIdOrNull(): Long? = instance.completedBy ?: instance.kidId

/** Appends a page, skipping instances already listed (a resubmitted chore can reappear). */
fun mergeReviewPage(existing: List<ReviewQueueItem>, page: List<ReviewQueueItem>): List<ReviewQueueItem> {
    val seen = existing.mapTo(HashSet()) { it.instance.id }
    return existing + page.filter { it.instance.id !in seen }
}

fun removeReviewItems(items: List<ReviewQueueItem>, ids: Set<Long>): List<ReviewQueueItem> =
    items.filter { it.instance.id !in ids }

fun toggleSelected(selected: Set<Long>, id: Long): Set<Long> = if (id in selected) selected - id else selected + id

/** Drops ids that are no longer listed (after a reload or a removal). */
fun pruneSelection(selected: Set<Long>, items: List<ReviewQueueItem>): Set<Long> {
    val listed = items.mapTo(HashSet()) { it.instance.id }
    return selected.filterTo(LinkedHashSet()) { it in listed }
}

/** Whether a loaded queue should be reloaded at [now]; a clock that went backwards counts as stale. */
fun isQueueStale(loadedAt: Instant?, now: Instant): Boolean =
    loadedAt == null || now.isBefore(loadedAt) || Duration.between(loadedAt, now) > QUEUE_STALE_AFTER

/** True when the photo's signed link has lapsed at [now]: the queue should be reloaded for a fresh one. */
fun EvidencePhoto.linkLapsed(now: Instant): Boolean = urlExpiresAt?.toInstant()?.isBefore(now) == true

/** A photo link worth opening: the full image unless it has expired. */
fun EvidencePhoto.openableUrl(): String? = if (expired == true) null else (url ?: thumbUrl)?.toString()

/** Codes meaning the chore is no longer waiting for approval, so it should leave the list. */
private val GONE_CODES = setOf("already_approved", "instance_not_submitted", "instance_not_found")

fun isGone(code: String?): Boolean = code != null && code in GONE_CODES

data class ApprovalFailure(val instanceId: Long, val code: String, val message: String)

data class ApprovalSummary(
    val approved: List<Long> = emptyList(),
    /** Not approved by this request, but no longer submitted (someone else decided first). */
    val alreadyDecided: List<Long> = emptyList(),
    val failed: List<ApprovalFailure> = emptyList(),
) {
    /** Ids to take off the list after a bulk approval. */
    val removable: Set<Long> get() = (approved + alreadyDecided).toSet()

    operator fun plus(other: ApprovalSummary) = ApprovalSummary(
        approved + other.approved,
        alreadyDecided + other.alreadyDecided,
        failed + other.failed,
    )
}

fun summarizeApprovals(results: List<ReviewQueueApprovalResult>): ApprovalSummary {
    val approved = mutableListOf<Long>()
    val decided = mutableListOf<Long>()
    val failed = mutableListOf<ApprovalFailure>()
    for (r in results) {
        when {
            r.approved -> approved += r.instanceId
            isGone(r.code) -> decided += r.instanceId
            else -> failed += ApprovalFailure(r.instanceId, r.code ?: "error", r.message.orEmpty())
        }
    }
    return ApprovalSummary(approved, decided, failed)
}

/**
 * Distinct ids in ascending order, split into requests of at most [BULK_APPROVE_MAX]. Sorting makes
 * the body canonical, so the same selection in any order sends the same bytes under the same key.
 */
fun bulkChunks(ids: Collection<Long>): List<List<Long>> = ids.distinct().sorted().chunked(BULK_APPROVE_MAX)

/** The first [max] code points of the string (the server counts characters, not UTF-16 units). */
fun String.takeCodePoints(max: Int): String =
    if (codePointCount(0, length) <= max) this else substring(0, offsetByCodePoints(0, max))

/** A rejection needs a reason, text, or both; null when it has neither. */
fun buildRejection(reason: RejectChoreInstanceInputBody.Reason?, text: String): RejectChoreInstanceInputBody? {
    val trimmed = text.trim().takeCodePoints(REJECT_TEXT_MAX).trim()
    if (reason == null && trimmed.isEmpty()) return null
    return RejectChoreInstanceInputBody(reason = reason, text = trimmed.ifEmpty { null })
}

/**
 * Idempotency-Key policy: one key per action attempt, reused on every retry of the same action,
 * and dropped once the server has given a definitive answer, or when the queue is reloaded
 * ([clear]: the reload shows what really happened). The server deduplicates only JSON-body POSTs,
 * so the key matters for bulk approval and rejection; a single approval has no body and is safe to
 * repeat anyway (a second one answers `instance_not_submitted`). An action is named by what it sends
 * (kind, instance ids, reason and text), so an edited rejection or a different selection gets a
 * fresh key instead of replaying a request with another body. Kept in the app container, so a
 * retry after the screen was recreated still carries the original key.
 */
class AttemptKeys(private val newKey: () -> String = { UUID.randomUUID().toString() }) {
    private val open = HashMap<String, String>()

    @Synchronized
    fun begin(action: String): String = open.getOrPut(action, newKey)

    /** Forgets every kept key, e.g. after a successful reload of the queue. */
    @Synchronized
    fun clear() = open.clear()

    /** Call with the outcome: the key is kept only when the server may not have seen the request. */
    @Synchronized
    fun finish(action: String, outcomeKnown: Boolean) {
        if (outcomeKnown) open.remove(action)
    }
}

fun approveAction(instanceId: Long) = "approve:$instanceId"

fun approveAllAction(ids: List<Long>) = "approve-all:" + ids.sorted().joinToString(",")

fun rejectAction(instanceId: Long, body: RejectChoreInstanceInputBody) =
    "reject:$instanceId:${body.reason?.value.orEmpty()}:${body.text.orEmpty()}"
