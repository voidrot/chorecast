package dev.voidrot.chorecast.ui.device

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.ViewModelStoreOwner
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.api.models.DevicePicker
import dev.voidrot.chorecast.api.models.KidProfile
import dev.voidrot.chorecast.api.models.ParentProfile
import dev.voidrot.chorecast.core.ActiveSession
import dev.voidrot.chorecast.core.DeviceClient
import dev.voidrot.chorecast.core.DeviceSessionToken
import dev.voidrot.chorecast.core.Heartbeat
import dev.voidrot.chorecast.core.HeartbeatStep
import dev.voidrot.chorecast.core.InMemoryTokenStore
import dev.voidrot.chorecast.core.ParentAuth
import dev.voidrot.chorecast.core.ParentPage
import dev.voidrot.chorecast.core.ParentReview
import dev.voidrot.chorecast.core.PinError
import dev.voidrot.chorecast.core.SessionCheck
import dev.voidrot.chorecast.core.SessionLock
import dev.voidrot.chorecast.core.ServerUrl
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

/** Who a PIN is being asked for. */
sealed interface PinTarget {
    data class Kid(val kid: KidProfile) : PinTarget
    data class Parent(val parent: ParentProfile) : PinTarget
}

/** A kid looking at their chores, with the token to ask the API with (their session, or their own device's). */
data class KidAccess(val kidId: Long, val name: String, val token: String)

/** Holds the view models of one kid's session, so they survive rotation but not the session. */
class KidSessionStore(val access: KidAccess) : ViewModelStoreOwner {
    override val viewModelStore = ViewModelStore()
}

/**
 * Holds the view models of one parent elevation, so they survive rotation but not the elevation. The
 * parent screens read the elevated token from [tokens], an in-memory store of their own: when the
 * server refuses it they clear that store, never the device's pairing token. [review] is new for each
 * elevation, so the idempotency keys of its writes die with it.
 */
class ParentSessionStore(val session: ActiveSession.Parent, val review: ParentReview) : ViewModelStoreOwner {
    override val viewModelStore = ViewModelStore()
    val tokens = InMemoryTokenStore(session.token)
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
    /** Which parent screen shows while a parent is elevated. */
    val parentPage: ParentPage = ParentPage.Overview,
    /** Where the open elevation's parent screens keep their view models; set and cleared with [active]. */
    val parentStore: ParentSessionStore? = null,
)

/**
 * A paired device: a kid's own device, or a shared tablet with a profile picker. Kid sessions lock
 * back to the picker after [SessionLock.KID_IDLE] without a touch; parent elevations end when they expire.
 */
class DeviceViewModel(
    private val url: ServerUrl,
    private val client: DeviceClient,
    private val auth: ParentAuth,
    private val tokens: TokenStore,
    private val appVersion: String,
    private val newParentReview: () -> ParentReview,
    now: () -> Instant = Instant::now,
    elapsedNanos: () -> Long = System::nanoTime,
) : ViewModel() {
    private val _state = MutableStateFlow(DeviceState())
    val state: StateFlow<DeviceState> = _state.asStateFlow()
    private var lockJob: Job? = null
    private var kidSession: KidSessionStore? = null
    private var parentSession: ParentSessionStore? = null
    private val sessionLock = SessionLock(now, elapsedNanos)

    /** The device token the kid home already re-checked after a 401, so a persistent 401 can't loop. */
    private var recheckedToken: String? = null

    init {
        load()
        heartbeat()
    }

    /** See [Heartbeat.step]: a parent's remote lock or a revoked device ends up here. */
    private fun heartbeat() = viewModelScope.launch {
        var ticks = 0
        while (true) {
            delay(Heartbeat.INTERVAL)
            ticks++
            val active = _state.value.active
            when (Heartbeat.step(ticks, active != null, _state.value.loading)) {
                HeartbeatStep.CheckSession -> if (active != null && Heartbeat.sessionEnded(auth.check(url, active.token))) endLocally(active)
                HeartbeatStep.RefreshPicker -> refreshPicker()
                HeartbeatStep.Nothing -> Unit
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

    private fun clearParentStore() {
        parentSession?.viewModelStore?.clear()
        parentSession = null
    }

    private fun clearSessionStores() {
        clearKidStore()
        clearParentStore()
    }

    override fun onCleared() = clearSessionStores()

    fun openParentPage(page: ParentPage) = _state.update { it.copy(parentPage = page) }

    private fun endLocally(active: ActiveSession) {
        if (_state.value.active != active) return
        sessionLock.close()
        lockJob?.cancel()
        _state.update { it.copy(active = null, parentPage = ParentPage.Overview, parentStore = null) }
        clearSessionStores()
    }

    private suspend fun refreshPicker() {
        val token = tokens.read() ?: return
        try {
            val picker = client.picker(url, token, appVersion)
            if (picker == null) {
                tokens.clear()
                _state.update { it.copy(unpaired = true) }
                clearSessionStores()
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
        clearSessionStores()
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
            val started = (result as? SessionResult.Started)?.let { toActive(it.session) }
            if (started != null) sessionLock.open(started)
            // A new elevation gets a fresh store, so no parent screen state outlives the elevation it was loaded under.
            clearParentStore()
            val store = (started as? ActiveSession.Parent)?.let { ParentSessionStore(it, newParentReview()) }
            parentSession = store
            if (result is SessionResult.Unpaired) tokens.clear()
            _state.update {
                val idle = it.copy(busy = false)
                when {
                    started != null -> idle.copy(pinTarget = null, active = started, parentPage = ParentPage.Overview, parentStore = store)
                    result is SessionResult.Unpaired -> idle.copy(unpaired = true)
                    else -> idle.copy(pinError = PinError.from(result))
                }
            }
            if (result is SessionResult.Unpaired) clearSessionStores()
            scheduleLock()
        }
    }

    /** Any touch while a kid session is open pushes the auto-lock back; a parent elevation is not extended. */
    fun onInteraction() = sessionLock.touch()

    /** Waits until the session is due, then locks. A touch moves the due time, so it checks again on waking. */
    private fun scheduleLock() {
        lockJob?.cancel()
        val wait = sessionLock.remaining() ?: return
        lockJob = viewModelScope.launch {
            delay(wait)
            if (sessionLock.due()) lock() else scheduleLock()
        }
    }

    /** Back to the picker; the session token is revoked on the server too. */
    fun lock() {
        lockJob?.cancel()
        val active = _state.value.active ?: return
        sessionLock.close()
        _state.update { it.copy(active = null, parentPage = ParentPage.Overview, parentStore = null) }
        clearSessionStores()
        viewModelScope.launch { client.lock(url, active.token) }
    }
}
