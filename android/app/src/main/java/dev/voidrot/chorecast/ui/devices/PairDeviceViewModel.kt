package dev.voidrot.chorecast.ui.devices

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.api.models.Kid
import dev.voidrot.chorecast.core.CreatePairingResult
import dev.voidrot.chorecast.core.DeviceClient
import dev.voidrot.chorecast.core.IssuedPairing
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.TokenStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.io.IOException

data class PairDeviceState(
    val loadingKids: Boolean = true,
    val kids: List<Kid> = emptyList(),
    /** The chosen kid's id; null means a shared family device. */
    val kidId: Long? = null,
    val creating: Boolean = false,
    /** The code on show; null while choosing. */
    val pairing: IssuedPairing? = null,
    /** Why the kids or the code couldn't be fetched: a connection message or a server error code. */
    val error: String? = null,
    val signedOut: Boolean = false,
)

/** A parent creates a pairing code for a kid's device or a shared tablet and shows it as a QR code. */
class PairDeviceViewModel(
    private val url: ServerUrl,
    private val client: DeviceClient,
    private val tokens: TokenStore,
) : ViewModel() {
    private val _state = MutableStateFlow(PairDeviceState())
    val state: StateFlow<PairDeviceState> = _state.asStateFlow()

    init {
        loadKids()
    }

    fun loadKids() {
        val token = tokens.read() ?: return _state.update { it.copy(signedOut = true) }
        _state.update { it.copy(loadingKids = true, error = null) }
        viewModelScope.launch {
            try {
                val kids = client.kids(url, token)
                _state.update {
                    if (kids == null) it.copy(loadingKids = false, signedOut = true) else it.copy(loadingKids = false, kids = kids)
                }
            } catch (e: IOException) {
                _state.update { it.copy(loadingKids = false, error = e.message.orEmpty()) }
            }
        }
    }

    fun choose(kidId: Long?) = _state.update { it.copy(kidId = kidId) }

    /** Creates a code for the chosen device. Also the refresh after expiry: it always makes a new code. */
    fun create() {
        val token = tokens.read() ?: return _state.update { it.copy(signedOut = true) }
        if (_state.value.creating) return
        _state.update { it.copy(creating = true, error = null) }
        viewModelScope.launch {
            val result = client.createPairing(url, token, _state.value.kidId)
            _state.update {
                when (result) {
                    is CreatePairingResult.Created -> it.copy(creating = false, pairing = result.pairing)
                    CreatePairingResult.SignedOut -> it.copy(creating = false, signedOut = true)
                    is CreatePairingResult.Unreachable -> it.copy(creating = false, error = result.reason)
                    is CreatePairingResult.Failed -> it.copy(creating = false, error = result.code)
                }
            }
        }
    }

    /** Back to the choice, dropping the code on show. */
    fun reset() = _state.update { it.copy(pairing = null, error = null) }
}
