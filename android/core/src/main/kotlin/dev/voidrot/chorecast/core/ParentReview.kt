package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.InstancesApi
import dev.voidrot.chorecast.api.apis.KidsApi
import dev.voidrot.chorecast.api.apis.PointsApi
import dev.voidrot.chorecast.api.apis.ReviewApi
import dev.voidrot.chorecast.api.infrastructure.HttpResponse
import dev.voidrot.chorecast.api.models.ApproveReviewQueueInputBody
import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.ChoreInstanceNoteInputBody
import dev.voidrot.chorecast.api.models.DailyStreak
import dev.voidrot.chorecast.api.models.Kid
import dev.voidrot.chorecast.api.models.PointsAdjustmentInput
import dev.voidrot.chorecast.api.models.PointsBalance
import dev.voidrot.chorecast.api.models.PointsEntry
import dev.voidrot.chorecast.api.models.Problem
import dev.voidrot.chorecast.api.models.ReassignChoreInstanceInputBody
import dev.voidrot.chorecast.api.models.RejectChoreInstanceInputBody
import dev.voidrot.chorecast.api.models.ReviewQueueItem
import io.ktor.client.engine.HttpClientEngine
import io.ktor.util.reflect.typeInfo
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import java.io.IOException
import java.time.LocalDate

/** One page of the review queue; [nextCursor] is null on the last page. */
data class ReviewPage(val items: List<ReviewQueueItem>, val nextCursor: String?)

/** A kid's chores with what the parent actions need: all kids (to reassign) and the balance. */
data class KidDayData(
    val kid: Kid,
    val kids: List<Kid>,
    val instances: List<ChoreInstance>,
    val balance: PointsBalance?,
)

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
        is Decision.Failed -> failureOutcomeKnown(code)
        else -> true
    }

/** An earlier request with the same key is still running: the outcome is not known yet. */
private const val CODE_IN_PROGRESS = "idempotency_key_in_progress"

/** A failure code that is a definitive answer, not a server error, a surprise or a request still running. */
internal fun failureOutcomeKnown(code: String): Boolean =
    !code.startsWith("http_5") && code != "unexpected" && code != CODE_IN_PROGRESS

/**
 * Maps a failed approval or rejection (single or bulk) to a [Decision]. Only `instance_not_submitted`
 * and `instance_not_found` mean "already decided". Any other 404 or 409, such as an in-progress key,
 * is a failure that keeps the chore (and the key, when the outcome is unknown).
 */
fun decisionFor(status: Int, code: String): Decision = when {
    status == 401 -> Decision.Unauthorized
    status == 403 -> Decision.Forbidden
    (status == 404 || status == 409) && (code == "instance_not_submitted" || code == "instance_not_found") ->
        Decision.AlreadyDecided
    status == 404 || status == 409 -> Decision.Failed(code)
    status == 422 -> Decision.Invalid(code)
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
        keys.clear(::isReviewAction) // the reload shows what happened to any attempt whose answer was lost
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

    /**
     * A kid's chores from [from] to [to] and what the actions need: the kid list for reassigning and
     * the kid's balance. Only the kid and the chores are required. The attempt keys are not cleared
     * here: a balance read can't show whether an adjustment whose answer was lost landed.
     */
    suspend fun kidDay(url: ServerUrl, token: String, kidId: Long, from: LocalDate, to: LocalDate): Fetch<KidDayData> =
        guard {
            coroutineScope {
                val kidsCall = async { kids(url, token).listKids() }
                val balanceCall = async { optional { points(url, token).getKidPointsBalance(kidId.toString()) } }
                val resp = instances(url, token).listChoreInstances(kidId, from.toString(), to.toString())
                val kidsResp = kidsCall.await()
                if (!kidsResp.success) return@coroutineScope kidsResp.failure()
                if (!resp.success) return@coroutineScope resp.failure()
                val all = kidsResp.body().items
                val kid = all.firstOrNull { it.id == kidId } ?: return@coroutineScope Fetch.NotFound
                Fetch.Ok(KidDayData(kid, all, resp.body().items, balanceCall.await()))
            }
        }

    /** Adds or subtracts points by hand; [body] comes from [buildAdjustment]. */
    suspend fun adjust(url: ServerUrl, token: String, kidId: Long, body: PointsAdjustmentInput): ActionResult<PointsEntry> =
        act(adjustAction(kidId, body)) { key -> points(url, token).createKidPointsAdjustment(kidId.toString(), body, key) }

    suspend fun excuse(url: ServerUrl, token: String, instance: ChoreInstance, body: ChoreInstanceNoteInputBody): ActionResult<ChoreInstance> =
        act(excuseAction(instance, body)) { key -> instances(url, token).excuseChoreInstance(instance.id, key, body) }

    suspend fun reverseApproval(
        url: ServerUrl,
        token: String,
        instance: ChoreInstance,
        body: ChoreInstanceNoteInputBody,
    ): ActionResult<ChoreInstance> =
        act(reverseAction(instance, body)) { key -> instances(url, token).reverseChoreInstanceApproval(instance.id, key, body) }

    suspend fun reassign(url: ServerUrl, token: String, instanceId: Long, kidId: Long): ActionResult<ChoreInstance> =
        act(reassignAction(instanceId, kidId)) { key ->
            instances(url, token).reassignChoreInstance(instanceId, ReassignChoreInstanceInputBody(kidId), key)
        }

    private suspend fun <T : Any> act(action: String, call: suspend (key: String) -> HttpResponse<T>): ActionResult<T> {
        val key = keys.begin(action)
        val result = try {
            val resp = call(key)
            if (resp.success) ActionResult.Done(resp.body()) else actionFailureFor(resp.status, resp.problemCode())
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            ActionResult.Unreachable(e.message ?: e::class.simpleName.orEmpty())
        } catch (e: Exception) {
            ActionResult.Failed("unexpected")
        }
        keys.finish(action, result.outcomeKnown)
        return result
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
