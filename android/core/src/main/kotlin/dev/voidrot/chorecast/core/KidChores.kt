package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.ChoresApi
import dev.voidrot.chorecast.api.apis.InstancesApi
import dev.voidrot.chorecast.api.apis.PointsApi
import dev.voidrot.chorecast.api.infrastructure.HttpResponse
import dev.voidrot.chorecast.api.models.Chore
import dev.voidrot.chorecast.api.models.DailyStreak
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

/**
 * What the kid's home and today screens show. [chores] is empty when the chore list couldn't be read
 * (rows then have no points); [balance] and [streak] are null when theirs couldn't.
 */
data class KidToday(
    val plan: TodayPlan,
    val chores: Map<Long, Chore>,
    val balance: PointsBalance?,
    val streak: DailyStreak?,
)

/**
 * A kid's chores, as the kid's device token or kid session token sees them. The server limits a kid
 * to their own instances, points and streaks; chore definitions (`chores:read`) are household-wide.
 */
class KidChores(private val engine: HttpClientEngine? = null) {
    private fun instances(url: ServerUrl, token: String) =
        InstancesApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    private fun chores(url: ServerUrl, token: String) =
        ChoresApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    private fun points(url: ServerUrl, token: String) =
        PointsApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }

    /**
     * Today's chores plus the next days (the server's default range), with points, balance and streak.
     * Only the instance list is required; the rest degrade to missing values.
     */
    suspend fun today(url: ServerUrl, token: String, kidId: Long): Fetch<KidToday> = guard {
        coroutineScope {
            val list = async { instances(url, token).listChoreInstances(kidId = kidId) }
            val defs = async { optional { chores(url, token).listChores() } }
            val balance = async { optional { points(url, token).getKidPointsBalance(kidId.toString()) } }
            val streaks = async { optional { points(url, token).getKidStreaks(kidId.toString()) } }

            val resp = list.await()
            if (!resp.success) return@coroutineScope resp.failure()
            val byId = defs.await()?.items.orEmpty().associateBy { it.id }
            Fetch.Ok(
                KidToday(
                    plan = buildTodayPlan(resp.body().items, byId),
                    chores = byId,
                    balance = balance.await(),
                    streak = streaks.await()?.daily,
                ),
            )
        }
    }

    /** One chore with its steps and evidence rules. */
    suspend fun chore(url: ServerUrl, token: String, choreId: Long): Fetch<Chore> = guard {
        val resp = chores(url, token).getChore(choreId)
        if (resp.success) Fetch.Ok(resp.body()) else resp.failure()
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
