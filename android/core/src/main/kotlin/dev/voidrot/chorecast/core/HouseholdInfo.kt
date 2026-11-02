package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.HouseholdApi
import dev.voidrot.chorecast.api.models.HouseholdSettings
import io.ktor.client.engine.HttpClientEngine
import kotlinx.coroutines.CancellationException

/** Reads the household's settings (name, timezone, day end...) for display in the app. */
class HouseholdInfo(private val engine: HttpClientEngine? = null) {
    /** The settings, or null when the server can't be reached or refuses the token. */
    suspend fun get(url: ServerUrl, token: String): HouseholdSettings? = (fetch(url, token) as? Fetch.Ok)?.value

    /** Like [get], but says why there are no settings: [Fetch.Unauthorized] when the token is refused. */
    suspend fun fetch(url: ServerUrl, token: String): Fetch<HouseholdSettings> = try {
        val resp = HouseholdApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }
            .getHousehold()
        when {
            resp.success -> Fetch.Ok(resp.body())
            resp.status == 401 || resp.status == 403 -> Fetch.Unauthorized
            else -> Fetch.Failed("http_${resp.status}")
        }
    } catch (e: CancellationException) {
        throw e
    } catch (e: java.io.IOException) {
        Fetch.Unreachable(e.message ?: e::class.simpleName.orEmpty())
    } catch (e: Exception) {
        Fetch.Failed(e.message ?: e::class.simpleName.orEmpty())
    }
}
