package dev.voidrot.chorecast.ui.devices

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.api.models.ListedDevice
import dev.voidrot.chorecast.core.DeviceClient
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.TokenStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.io.IOException

data class DevicesState(
    val loading: Boolean = true,
    val devices: List<ListedDevice> = emptyList(),
    val error: String? = null,
    val signedOut: Boolean = false,
    /** Asking to confirm unpairing this device. */
    val confirmRevoke: ListedDevice? = null,
    /** A short confirmation to show once ("Tab goes back to its picker"). */
    val notice: Notice? = null,
)

sealed interface Notice {
    data class Locked(val name: String) : Notice
    data class Revoked(val name: String) : Notice
    data object Failed : Notice
}

/** The parent's devices list: lock a shared device back to its picker, or unpair a device. */
class DevicesViewModel(
    private val url: ServerUrl,
    private val client: DeviceClient,
    private val tokens: TokenStore,
) : ViewModel() {
    private val _state = MutableStateFlow(DevicesState())
    val state: StateFlow<DevicesState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        val token = tokens.read() ?: return _state.update { it.copy(signedOut = true) }
        _state.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            try {
                val list = client.list(url, token)
                _state.update {
                    if (list == null) it.copy(loading = false, signedOut = true) else it.copy(loading = false, devices = list)
                }
            } catch (e: IOException) {
                _state.update { it.copy(loading = false, error = e.message.orEmpty()) }
            }
        }
    }

    fun lock(d: ListedDevice) = act(Notice.Locked(d.name)) { client.lockRemotely(url, it, d.id) }

    fun askRevoke(d: ListedDevice?) = _state.update { it.copy(confirmRevoke = d) }

    fun revoke() {
        val d = _state.value.confirmRevoke ?: return
        _state.update { it.copy(confirmRevoke = null) }
        act(Notice.Revoked(d.name)) { client.revoke(url, it, d.id) }
    }

    fun noticeShown() = _state.update { it.copy(notice = null) }

    private fun act(success: Notice, call: suspend (String) -> Boolean) {
        val token = tokens.read() ?: return
        viewModelScope.launch {
            val ok = call(token)
            _state.update { it.copy(notice = if (ok) success else Notice.Failed) }
            load()
        }
    }
}
