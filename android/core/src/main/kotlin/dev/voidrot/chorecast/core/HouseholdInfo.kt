package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.apis.HouseholdApi
import dev.voidrot.chorecast.api.models.HouseholdSettings
import io.ktor.client.engine.HttpClientEngine
import kotlinx.coroutines.CancellationException

/** Reads the household's settings (name, timezone, day end...) for display in the app. */
class HouseholdInfo(private val engine: HttpClientEngine? = null) {
    /** The settings, or null when the server can't be reached or refuses the token. */
    suspend fun get(url: ServerUrl, token: String): HouseholdSettings? = try {
        val resp = HouseholdApi(baseUrl = url.apiBase, httpClientEngine = engine, httpClientConfig = ApiClients.config)
            .apply { setBearerToken(token) }
            .getHousehold()
        if (resp.success) resp.body() else null
    } catch (e: CancellationException) {
        throw e
    } catch (_: Exception) {
        null
    }
}
