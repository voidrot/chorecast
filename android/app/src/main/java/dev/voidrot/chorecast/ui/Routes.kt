package dev.voidrot.chorecast.ui

import kotlinx.serialization.Serializable

/** Startup: checks the saved server and sign-in before showing anything else. */
@Serializable
data object StartupRoute

/** Enter or change the server URL. */
@Serializable
data object ConnectRoute

/** Parent sign-in on [serverUrl]. */
@Serializable
data class LoginRoute(val serverUrl: String)

/**
 * Placeholder parent home until the parent screens (#32) and device pairing (#18) exist.
 * [recoveryCodesLeft] is set right after a sign-in that used a recovery code.
 */
@Serializable
data class HomeRoute(val serverUrl: String, val recoveryCodesLeft: Long? = null)
