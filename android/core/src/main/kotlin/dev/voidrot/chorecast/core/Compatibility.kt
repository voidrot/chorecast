package dev.voidrot.chorecast.core

/** The API major version this app speaks (the `/api/vN` it calls). */
const val SUPPORTED_API_VERSION: Long = 1

/** Whether this app can talk to a server, from the server's reported `apiVersion`. */
sealed interface Compatibility {
    data object Compatible : Compatibility

    /** The server speaks a newer API major: the app needs updating. */
    data class AppTooOld(val serverApiVersion: Long) : Compatibility

    /** The server speaks an older API major: the server needs updating. */
    data class ServerTooOld(val serverApiVersion: Long) : Compatibility

    companion object {
        fun of(serverApiVersion: Long, supported: Long = SUPPORTED_API_VERSION): Compatibility = when {
            serverApiVersion == supported -> Compatible
            serverApiVersion > supported -> AppTooOld(serverApiVersion)
            else -> ServerTooOld(serverApiVersion)
        }
    }
}
