package dev.voidrot.chorecast.ui

import kotlinx.serialization.Serializable

/** Startup: checks the saved server before showing anything else. */
@Serializable
data object StartupRoute

/** Enter or change the server URL. */
@Serializable
data object ConnectRoute

/** Placeholder home until pairing (#18) and the kid/parent screens exist. */
@Serializable
data class HomeRoute(val serverUrl: String, val serverVersion: String)
