package dev.voidrot.chorecast.ui

import kotlinx.serialization.Serializable

/** Startup: checks the saved server and sign-in before showing anything else. */
@Serializable
data object StartupRoute

/** First launch: pair this device, or sign in as a parent. */
@Serializable
data object WelcomeRoute

/** Scan (or type) a pairing code. */
@Serializable
data object PairRoute

/** A paired kid or shared device: kid home, or the shared profile picker. */
@Serializable
data class DeviceRoute(val serverUrl: String)

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

/** The parent's paired-devices list. */
@Serializable
data class DevicesRoute(val serverUrl: String)
