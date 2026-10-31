package dev.voidrot.chorecast.data

import dev.voidrot.chorecast.core.Pkce
import dev.voidrot.chorecast.core.SsoCallback
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Carries a single sign-on attempt across the trip to the browser: the PKCE verifier stays here
 * (in memory: if Android kills the app meanwhile, the attempt is simply retried), and the
 * activity hands over the `chorecast://auth` redirect.
 */
class SsoBridge {
    var pending: Pkce? = null

    private val _callback = MutableStateFlow<SsoCallback?>(null)
    val callback: StateFlow<SsoCallback?> = _callback.asStateFlow()

    fun deliver(callback: SsoCallback) {
        _callback.value = callback
    }

    /** Takes the delivered callback and the verifier that goes with it, once. */
    fun consume(): Pair<SsoCallback, Pkce?>? {
        val cb = _callback.value ?: return null
        _callback.value = null
        val pkce = pending
        pending = null
        return cb to pkce
    }
}
