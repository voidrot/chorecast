package dev.voidrot.chorecast.ui.device

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.api.models.DevicePicker
import dev.voidrot.chorecast.api.models.KidProfile
import dev.voidrot.chorecast.api.models.ParentProfile
import dev.voidrot.chorecast.core.DeviceClient
import dev.voidrot.chorecast.core.DeviceSessionToken
import dev.voidrot.chorecast.core.ParentAuth
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.SessionCheck
import dev.voidrot.chorecast.core.SessionResult
import dev.voidrot.chorecast.core.TokenStore
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.io.IOException
import java.time.Instant
import kotlin.time.Duration
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.minutes

/** Who a PIN is being asked for. */
sealed interface PinTarget {
    data class Kid(val kid: KidProfile) : PinTarget
    data class Parent(val parent: ParentProfile) : PinTarget
}

sealed interface PinError {
    data object Wrong : PinError
    data class Locked(val minutes: Long) : PinError
    data class Failed(val reason: String) : PinError
}

/** A session open on this device. */
sealed interface ActiveSession {
    val token: String

    data class Kid(val kid: KidProfile, override val token: String) : ActiveSession
    data class Parent(val parent: ParentProfile, override val token: String, val expiresAt: Instant) : ActiveSession
}

data class DeviceState(
    val loading: Boolean = true,
    /** The device token stopped working (revoked, or the kid was archived): pair again. */
    val unpaired: Boolean = false,
    val offline: String? = null,
    val picker: DevicePicker? = null,
    /** Kid-bound devices: the kid's name. */
    val kidName: String? = null,
    val choosingParent: Boolean = false,
    val pinTarget: PinTarget? = null,
    val pinError: PinError? = null,
    val busy: Boolean = false,
    val active: ActiveSession? = null,
)

/**
 * A paired device: a kid's own device, or a shared tablet with a profile picker. Kid sessions lock
 * back to the picker after [KID_IDLE] without a touch; parent elevations end when they expire.
 */
class DeviceViewModel(
    private val url: ServerUrl,
    private val client: DeviceClient,
    private val auth: ParentAuth,
    private val tokens: TokenStore,
    private val now: () -> Instant = Instant::now,
) : ViewModel() {
    private val _state = MutableStateFlow(DeviceState())
    val state: StateFlow<DeviceState> = _state.asStateFlow()
    private var lockJob: Job? = null

    init {
        load()
    }

    fun load() {
        val token = tokens.read() ?: return _state.update { it.copy(loading = false, unpaired = true) }
        _state.update { it.copy(loading = true, offline = null) }
        viewModelScope.launch {
            try {
                val picker = client.picker(url, token)
                if (picker == null) {
                    tokens.clear()
                    _state.update { it.copy(loading = false, unpaired = true) }
                    return@launch
                }
                val kidName = if (!picker.device.shared) {
                    (auth.check(url, token) as? SessionCheck.SignedIn)?.me?.displayName
                } else {
                    null
                }
                _state.update { it.copy(loading = false, picker = picker, kidName = kidName) }
            } catch (e: IOException) {
                _state.update { it.copy(loading = false, offline = e.message.orEmpty()) }
            }
        }
    }

    fun pickKid(kid: KidProfile) {
        if (kid.pinRequired) {
            _state.update { it.copy(pinTarget = PinTarget.Kid(kid), pinError = null) }
        } else {
            startKid(kid, null)
        }
    }

    fun chooseParent(on: Boolean) = _state.update { it.copy(choosingParent = on) }

    fun pickParent(parent: ParentProfile) =
        _state.update { it.copy(choosingParent = false, pinTarget = PinTarget.Parent(parent), pinError = null) }

    fun cancelPin() = _state.update { it.copy(pinTarget = null, pinError = null) }

    fun submitPin(pin: String) {
        when (val target = _state.value.pinTarget) {
            is PinTarget.Kid -> startKid(target.kid, pin)
            is PinTarget.Parent -> elevate(target.parent, pin)
            null -> Unit
        }
    }

    private fun startKid(kid: KidProfile, pin: String?) = session({ client.startKidSession(url, it, kid.id, pin) }) { s ->
        ActiveSession.Kid(kid, s.token)
    }

    private fun elevate(parent: ParentProfile, pin: String) = session({ client.elevate(url, it, parent.id, pin) }) { s ->
        ActiveSession.Parent(parent, s.token, s.expiresAt)
    }

    private fun session(call: suspend (String) -> SessionResult, toActive: (DeviceSessionToken) -> ActiveSession) {
        val token = tokens.read() ?: return
        _state.update { it.copy(busy = true, pinError = null) }
        viewModelScope.launch {
            val result = call(token)
            _state.update {
                val idle = it.copy(busy = false)
                when (result) {
                    is SessionResult.Started -> idle.copy(pinTarget = null, active = toActive(result.session))
                    SessionResult.WrongPin -> idle.copy(pinError = PinError.Wrong)
                    is SessionResult.Locked -> idle.copy(pinError = PinError.Locked(((result.retryAfterSeconds ?: 900) + 59) / 60))
                    SessionResult.Unpaired -> {
                        tokens.clear()
                        idle.copy(unpaired = true)
                    }
                    is SessionResult.Unreachable -> idle.copy(pinError = PinError.Failed(result.reason))
                    is SessionResult.Failed -> idle.copy(pinError = PinError.Failed(result.code))
                }
            }
            scheduleLock()
        }
    }

    /** Any touch while a kid session is open pushes the auto-lock back. */
    fun onInteraction() {
        if (_state.value.active is ActiveSession.Kid) scheduleLock()
    }

    private fun scheduleLock() {
        lockJob?.cancel()
        val wait: Duration = when (val a = _state.value.active) {
            is ActiveSession.Kid -> KID_IDLE
            is ActiveSession.Parent -> (a.expiresAt.toEpochMilli() - now().toEpochMilli()).coerceAtLeast(0).milliseconds
            null -> return
        }
        lockJob = viewModelScope.launch {
            delay(wait)
            lock()
        }
    }

    /** Back to the picker; the session token is revoked on the server too. */
    fun lock() {
        lockJob?.cancel()
        val active = _state.value.active ?: return
        _state.update { it.copy(active = null) }
        viewModelScope.launch { client.lock(url, active.token) }
    }

    companion object {
        val KID_IDLE = 2.minutes
    }
}
