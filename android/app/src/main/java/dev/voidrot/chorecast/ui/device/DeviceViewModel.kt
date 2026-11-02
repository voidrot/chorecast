package dev.voidrot.chorecast.ui.device

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.ViewModelStoreOwner
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
import kotlin.time.Duration.Companion.seconds

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

/** A kid looking at their chores, with the token to ask the API with (their session, or their own device's). */
data class KidAccess(val kidId: Long, val name: String, val token: String)

/** Holds the view models of one kid's session, so they survive rotation but not the session. */
class KidSessionStore(val access: KidAccess) : ViewModelStoreOwner {
    override val viewModelStore = ViewModelStore()
}

data class DeviceState(
    val loading: Boolean = true,
    /** The device token stopped working (revoked, or the kid was archived): pair again. */
    val unpaired: Boolean = false,
    val offline: String? = null,
    val picker: DevicePicker? = null,
    /** Kid-bound devices: the kid who owns this device. */
    val ownKid: KidAccess? = null,
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
    private val appVersion: String,
    private val now: () -> Instant = Instant::now,
) : ViewModel() {
    private val _state = MutableStateFlow(DeviceState())
    val state: StateFlow<DeviceState> = _state.asStateFlow()
    private var lockJob: Job? = null
    private var kidSession: KidSessionStore? = null

    /** The device token the kid home already re-checked after a 401, so a persistent 401 can't loop. */
    private var recheckedToken: String? = null

    init {
        load()
        heartbeat()
    }

    /**
     * Until push exists (Phase 3), the device notices remote changes by checking in: every 30 s
     * while a session is open (a parent's remote lock ends it), every 60 s at the picker (new kids,
     * or the device was unpaired).
     */
    private fun heartbeat() = viewModelScope.launch {
        var ticks = 0
        while (true) {
            delay(HEARTBEAT)
            ticks++
            val active = _state.value.active
            when {
                active != null -> if (auth.check(url, active.token) is SessionCheck.SignedOut) endLocally(active)
                ticks % 2 == 0 && !_state.value.loading -> refreshPicker()
            }
        }
    }

    /** The API refused the open kid session's token (a parent locked the device, or it expired): back to the picker. */
    fun sessionRefused() {
        _state.value.active?.let(::endLocally)
    }

    /**
     * A kid-bound device's token was refused by the chores API. Check in with the picker once: if the
     * device was unpaired, that finds out. If the picker still accepts the token, the error stays on
     * screen (with its retry button) instead of checking in again and again.
     */
    fun deviceTokenRefused() {
        val token = _state.value.ownKid?.token ?: return
        if (recheckedToken == token) return
        recheckedToken = token
        load()
    }

    /**
     * Where the kid's view models live. They are created for one kid session (or a kid-bound device)
     * and live here, not in the screen, so a rotation keeps them; the session ending clears them.
     */
    fun kidStore(access: KidAccess): ViewModelStoreOwner {
        kidSession?.takeIf { it.access == access }?.let { return it }
        clearKidStore()
        return KidSessionStore(access).also { kidSession = it }
    }

    private fun clearKidStore() {
        kidSession?.viewModelStore?.clear()
        kidSession = null
    }

    override fun onCleared() = clearKidStore()

    private fun endLocally(active: ActiveSession) {
        if (_state.value.active != active) return
        lockJob?.cancel()
        _state.update { it.copy(active = null) }
        clearKidStore()
    }

    private suspend fun refreshPicker() {
        val token = tokens.read() ?: return
        try {
            val picker = client.picker(url, token, appVersion)
            if (picker == null) {
                tokens.clear()
                _state.update { it.copy(unpaired = true) }
                clearKidStore()
            } else {
                _state.update { it.copy(picker = picker, offline = null) }
            }
        } catch (_: IOException) {
            // Offline: keep showing what we have.
        }
    }

    fun load() {
        val token = tokens.read() ?: return _state.update { it.copy(loading = false, unpaired = true) }
        // The kid home is replaced by the spinner, so its view models start over when it comes back.
        clearKidStore()
        _state.update { it.copy(loading = true, offline = null) }
        viewModelScope.launch {
            try {
                val picker = client.picker(url, token, appVersion)
                if (picker == null) {
                    tokens.clear()
                    _state.update { it.copy(loading = false, unpaired = true) }
                    return@launch
                }
                val kidId = picker.device.kidId
                val ownKid = if (!picker.device.shared && kidId != null) {
                    val name = (auth.check(url, token) as? SessionCheck.SignedIn)?.me?.displayName
                    KidAccess(kidId, name.orEmpty(), token)
                } else {
                    null
                }
                _state.update { it.copy(loading = false, picker = picker, ownKid = ownKid) }
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
            if (result is SessionResult.Unpaired) clearKidStore()
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
        clearKidStore()
        viewModelScope.launch { client.lock(url, active.token) }
    }

    companion object {
        val KID_IDLE = 2.minutes
        val HEARTBEAT = 30.seconds
    }
}
