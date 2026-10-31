package dev.voidrot.chorecast.ui.pair

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.core.DeviceClient
import dev.voidrot.chorecast.core.PairResult
import dev.voidrot.chorecast.core.PairingQr
import dev.voidrot.chorecast.core.Result
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.TokenStore
import dev.voidrot.chorecast.data.AppMode
import dev.voidrot.chorecast.data.SettingsStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

sealed interface PairError {
    data object NotAPairingCode : PairError
    data object InvalidServer : PairError
    data object InvalidCode : PairError
    data object RateLimited : PairError
    data class Unreachable(val reason: String) : PairError
    data class Failed(val code: String) : PairError
}

data class PairState(
    val manual: Boolean = false,
    val server: String = "",
    val code: String = "",
    val pairing: Boolean = false,
    val error: PairError? = null,
    /** Bumped after each scan, so the scanner restarts (it stops at the first QR code it reads). */
    val scanAttempt: Int = 0,
    /** Set once paired. */
    val paired: Boolean = false,
)

/** Pairs this device from a scanned QR code, or a typed server address and code. */
class PairViewModel(
    private val settings: SettingsStore,
    private val tokens: TokenStore,
    private val client: DeviceClient,
    private val deviceName: String,
    private val appVersion: String,
) : ViewModel() {
    private val _state = MutableStateFlow(PairState(server = settings.serverUrl?.value.orEmpty()))
    val state: StateFlow<PairState> = _state.asStateFlow()

    fun showManual(on: Boolean) = _state.update { it.copy(manual = on, error = null) }
    fun onServer(v: String) = _state.update { it.copy(server = v, error = null) }
    fun onCode(v: String) = _state.update { it.copy(code = v, error = null) }

    fun onScanned(payload: String) {
        if (_state.value.pairing) return
        val qr = PairingQr.parse(payload)
            ?: return _state.update { it.copy(error = PairError.NotAPairingCode, scanAttempt = it.scanAttempt + 1) }
        pair(qr.server, qr.code)
    }

    fun submitManual() {
        val url = (ServerUrl.parse(_state.value.server) as? Result.Ok)?.value
            ?: return _state.update { it.copy(error = PairError.InvalidServer) }
        if (_state.value.code.isBlank()) return
        pair(url, _state.value.code)
    }

    private fun pair(url: ServerUrl, code: String) {
        _state.update { it.copy(pairing = true, error = null) }
        viewModelScope.launch {
            val result = client.pair(url, code, deviceName, appVersion)
            _state.update {
                val idle = it.copy(pairing = false, scanAttempt = it.scanAttempt + 1)
                when (result) {
                    is PairResult.Paired -> {
                        settings.serverUrl = url
                        settings.mode = AppMode.DEVICE
                        tokens.write(result.token)
                        idle.copy(paired = true)
                    }
                    PairResult.InvalidCode -> idle.copy(error = PairError.InvalidCode)
                    PairResult.RateLimited -> idle.copy(error = PairError.RateLimited)
                    is PairResult.Unreachable -> idle.copy(error = PairError.Unreachable(result.reason))
                    is PairResult.Failed -> idle.copy(error = PairError.Failed(result.code))
                }
            }
        }
    }
}
