package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.InstancesApi
import dev.voidrot.chorecast.api.apis.KidsApi
import dev.voidrot.chorecast.api.apis.PointsApi
import dev.voidrot.chorecast.api.infrastructure.HttpResponse
import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.DailyStreak
import dev.voidrot.chorecast.api.models.InstanceEvidence
import dev.voidrot.chorecast.api.models.Kid
import dev.voidrot.chorecast.api.models.PointsBalance
import io.ktor.client.engine.HttpClientEngine
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import java.io.IOException

/** The outcome of one read from the API. */
sealed interface Fetch<out T> {
    data class Ok<T>(val value: T) : Fetch<T>

    /** The token was refused (expired session, revoked device). */
    data object Unauthorized : Fetch<Nothing>
    data object NotFound : Fetch<Nothing>
    data class Unreachable(val reason: String) : Fetch<Nothing>
    data class Failed(val code: String) : Fetch<Nothing>
}

/** What the kid's home and today screens show. [balance] and [streak] are null when theirs couldn't be read. */
data class KidToday(
    val plan: TodayPlan,
    val balance: PointsBalance?,
    val streak: DailyStreak?,
)

/**
 * A kid's chores, as the kid's device token or kid session token sees them. The server limits a kid
 * to their own instances, evidence, points and streaks. Instances carry the chore's points, steps and
 * evidence rules, so the app never reads chore definitions (`chores:read`).
 */
class KidChores(private val engine: HttpClientEngine? = null) {
    private fun instances(url: ServerUrl, token: String) =
        InstancesApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    private fun points(url: ServerUrl, token: String) =
        PointsApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    /**
     * Today's chores plus the next days (the server's default range), with points, balance and streak.
     * Only the instance list is required; the balance and streak degrade to missing values.
     */
    suspend fun today(url: ServerUrl, token: String, kidId: Long): Fetch<KidToday> = guard {
        coroutineScope {
            val list = async { instances(url, token).listChoreInstances(kidId = kidId) }
            val balance = async { optional { points(url, token).getKidPointsBalance(kidId.toString()) } }
            val streaks = async { optional { points(url, token).getKidStreaks(kidId.toString()) } }

            val resp = list.await()
            if (!resp.success) return@coroutineScope resp.failure()
            Fetch.Ok(
                KidToday(
                    plan = buildTodayPlan(resp.body().items),
                    balance = balance.await(),
                    streak = streaks.await()?.daily,
                ),
            )
        }
    }

    /** Just the kid's instances (today and the days ahead), for the background sync that schedules reminders. */
    suspend fun upcoming(url: ServerUrl, token: String, kidId: Long): Fetch<List<ChoreInstance>> = guard {
        val resp = instances(url, token).listChoreInstances(kidId = kidId)
        if (resp.success) Fetch.Ok(resp.body().items) else resp.failure()
    }

    /** The kid's own profile, which carries the quiet hours and day-end override the reminders need. */
    suspend fun profile(url: ServerUrl, token: String, kidId: Long): Fetch<Kid> = guard {
        val resp = KidsApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }
            .getKid(kidId.toString())
        if (resp.success) Fetch.Ok(resp.body()) else resp.failure()
    }

    /** The instances dated in [range] (at most [HISTORY_PAGE_DAYS] days), planned and open ones included. */
    suspend fun instancesIn(url: ServerUrl, token: String, kidId: Long, range: DateRange): Fetch<List<ChoreInstance>> = guard {
        val resp = instances(url, token).listChoreInstances(kidId = kidId, from = range.from.toString(), to = range.to.toString())
        if (resp.success) Fetch.Ok(resp.body().items) else resp.failure()
    }

    /** Every submission for one instance, oldest first. The photo links are signed and short-lived, so read when needed. */
    suspend fun evidence(url: ServerUrl, token: String, instanceId: Long): Fetch<List<InstanceEvidence>> = guard {
        val resp = instances(url, token).getChoreInstanceEvidence(instanceId)
        if (resp.success) Fetch.Ok(resp.body().items) else resp.failure()
    }

    private fun HttpResponse<*>.failure(): Fetch<Nothing> = when (status) {
        401 -> Fetch.Unauthorized
        404 -> Fetch.NotFound
        else -> Fetch.Failed("http_$status")
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
