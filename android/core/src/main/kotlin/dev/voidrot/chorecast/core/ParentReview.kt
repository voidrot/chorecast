package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.InstancesApi
import dev.voidrot.chorecast.api.apis.KidsApi
import dev.voidrot.chorecast.api.apis.PointsApi
import dev.voidrot.chorecast.api.apis.ReviewApi
import dev.voidrot.chorecast.api.infrastructure.HttpResponse
import dev.voidrot.chorecast.api.models.ApproveReviewQueueInputBody
import dev.voidrot.chorecast.api.models.DailyStreak
import dev.voidrot.chorecast.api.models.PointsBalance
import dev.voidrot.chorecast.api.models.Problem
import dev.voidrot.chorecast.api.models.RejectChoreInstanceInputBody
import dev.voidrot.chorecast.api.models.ReviewQueueItem
import io.ktor.client.engine.HttpClientEngine
import io.ktor.util.reflect.typeInfo
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import java.io.IOException

/** One page of the review queue; [nextCursor] is null on the last page. */
data class ReviewPage(val items: List<ReviewQueueItem>, val nextCursor: String?)

/** The outcome of approving or rejecting one chore. */
sealed interface Decision {
    data object Done : Decision

    /** Someone else already approved or rejected it (or it was removed): drop it from the list. */
    data object AlreadyDecided : Decision

    /** The session was refused: sign in again. */
    data object Unauthorized : Decision
    data object Forbidden : Decision

    /** The server refused the input; [code] is its error code. */
    data class Invalid(val code: String) : Decision
    data class Unreachable(val reason: String) : Decision
    data class Failed(val code: String) : Decision
}

/** Whether the server has answered for sure, so the attempt's Idempotency-Key can be dropped. */
val Decision.outcomeKnown: Boolean
    get() = when (this) {
        is Decision.Unreachable -> false
        is Decision.Failed -> !code.startsWith("http_5") && code != "unexpected"
        else -> true
    }

/** Maps a failed single approval or rejection to a [Decision]. */
fun decisionFor(status: Int, code: String): Decision = when (status) {
    401 -> Decision.Unauthorized
    403 -> Decision.Forbidden
    404, 409 -> Decision.AlreadyDecided
    422 -> Decision.Invalid(code)
    else -> Decision.Failed("http_$status")
}

/**
 * What a bulk approval did. [summary] covers the chunks the server answered. [stopped] is why the
 * rest was not sent or not answered (null when every chunk was answered); [unsent] are those ids.
 */
data class BulkOutcome(val summary: ApprovalSummary, val stopped: Decision?, val unsent: List<Long>)

/**
 * The parent's side of reviewing: the home overview, the review queue and the decisions on it.
 * Writes send an Idempotency-Key from [keys], one per action attempt and reused on retry.
 */
class ParentReview(
    private val engine: HttpClientEngine? = null,
    private val keys: AttemptKeys = AttemptKeys(),
) {
    private fun kids(url: ServerUrl, token: String) =
        KidsApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    private fun instances(url: ServerUrl, token: String) =
        InstancesApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    private fun points(url: ServerUrl, token: String) =
        PointsApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    private fun review(url: ServerUrl, token: String) =
        ReviewApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    /**
     * Each kid's progress today, balance, streak and waiting reviews. Only the kid list is required;
     * everything else degrades to missing values.
     */
    suspend fun overview(url: ServerUrl, token: String): Fetch<ParentOverview> = guard {
        coroutineScope {
            val kidsCall = async { kids(url, token).listKids() }
            val instancesCall = async { optional { instances(url, token).listChoreInstances() } }
            val queueCall = async { optional { review(url, token).listReviewQueue(limit = BULK_APPROVE_MAX.toLong()) } }

            val resp = kidsCall.await()
            if (!resp.success) return@coroutineScope resp.failure()
            val kidList = resp.body().items
            val balances = kidList.map { k ->
                async { optional { points(url, token).getKidPointsBalance(k.id.toString()) }?.let { k.id to it } }
            }
            val streaks = kidList.map { k ->
                async { optional { points(url, token).getKidStreaks(k.id.toString()) }?.let { k.id to it.daily } }
            }
            val queue = queueCall.await()
            Fetch.Ok(
                buildParentOverview(
                    kids = kidList,
                    instances = instancesCall.await()?.items.orEmpty(),
                    balances = balances.awaitAll().filterNotNull().toMap<Long, PointsBalance>(),
                    streaks = streaks.awaitAll().filterNotNull().toMap<Long, DailyStreak>(),
                    queue = queue?.items.orEmpty(),
                    queueHasMore = queue?.nextCursor != null,
                ),
            )
        }
    }

    /** One page of the queue, oldest first; pass the previous page's cursor for the next. */
    suspend fun queue(url: ServerUrl, token: String, cursor: String? = null): Fetch<ReviewPage> = guard {
        val resp = review(url, token).listReviewQueue(cursor = cursor)
        if (!resp.success) return@guard resp.failure()
        val page = resp.body()
        Fetch.Ok(ReviewPage(page.items, page.nextCursor))
    }

    suspend fun approve(url: ServerUrl, token: String, instanceId: Long): Decision =
        decide(approveAction(instanceId)) { key -> instances(url, token).approveChoreInstance(instanceId, key) }

    suspend fun reject(
        url: ServerUrl,
        token: String,
        instanceId: Long,
        body: RejectChoreInstanceInputBody,
    ): Decision = decide(rejectAction(instanceId, body)) { key ->
        instances(url, token).rejectChoreInstance(instanceId, body, key)
    }

    /**
     * Approves [ids] in requests of at most [BULK_APPROVE_MAX], one after another. It stops at the
     * first request that was refused or not answered, because the rest would fail the same way.
     */
    suspend fun approveAll(url: ServerUrl, token: String, ids: Collection<Long>): BulkOutcome {
        var summary = ApprovalSummary()
        val chunks = bulkChunks(ids)
        for ((i, chunk) in chunks.withIndex()) {
            val action = approveAllAction(chunk)
            val key = keys.begin(action)
            val result = try {
                val resp = review(url, token).approveReviewQueue(ApproveReviewQueueInputBody(chunk), key)
                if (resp.success) null to resp.body().results else decisionFor(resp.status, resp.problemCode()) to null
            } catch (e: CancellationException) {
                throw e
            } catch (e: IOException) {
                Decision.Unreachable(e.message ?: e::class.simpleName.orEmpty()) to null
            } catch (e: Exception) {
                Decision.Failed("unexpected") to null
            }
            val (stopped, results) = result
            if (results != null) {
                keys.finish(action, outcomeKnown = true)
                summary += summarizeApprovals(results)
            } else {
                keys.finish(action, outcomeKnown = stopped!!.outcomeKnown)
                return BulkOutcome(summary, stopped, chunks.drop(i).flatten())
            }
        }
        return BulkOutcome(summary, null, emptyList())
    }

    private suspend fun decide(action: String, call: suspend (key: String) -> HttpResponse<*>): Decision {
        val key = keys.begin(action)
        val decision = try {
            val resp = call(key)
            if (resp.success) Decision.Done else decisionFor(resp.status, resp.problemCode())
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            Decision.Unreachable(e.message ?: e::class.simpleName.orEmpty())
        } catch (e: Exception) {
            Decision.Failed("unexpected")
        }
        keys.finish(action, decision.outcomeKnown)
        return decision
    }

    private fun HttpResponse<*>.failure(): Fetch<Nothing> = when (status) {
        401 -> Fetch.Unauthorized
        404 -> Fetch.NotFound
        else -> Fetch.Failed("http_$status")
    }

    private suspend fun HttpResponse<*>.problemCode(): String = try {
        typedBody<Problem>(typeInfo<Problem>()).code
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        "http_$status"
    }

    /** A side request that may fail without failing the screen; offline surfaces on the required one. */
    private suspend fun <T : Any> optional(block: suspend () -> HttpResponse<T>): T? = try {
        block().takeIf { it.success }?.body()
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        null
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
