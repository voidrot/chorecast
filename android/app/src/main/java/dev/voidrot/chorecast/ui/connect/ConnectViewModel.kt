package dev.voidrot.chorecast.ui.connect

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.core.Result
import dev.voidrot.chorecast.core.ServerChecker
import dev.voidrot.chorecast.core.ServerStatus
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.TokenStore
import dev.voidrot.chorecast.data.SettingsStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class ConnectState(
    val input: String = "",
    val checking: Boolean = false,
    val inputError: ServerUrl.Error? = null,
    val problem: ServerStatus? = null,
    val connected: ServerStatus.Ready? = null,
) {
    /** Warn before connecting to a plain-HTTP server. */
    val cleartext: Boolean get() = input.trim().startsWith("http://", ignoreCase = true)
}

/** Lets the user enter a server URL, checks it, and saves it once it's a compatible server. */
class ConnectViewModel(
    private val settings: SettingsStore,
    private val checker: ServerChecker,
    private val tokens: TokenStore,
) : ViewModel() {
    private val _state = MutableStateFlow(ConnectState(input = settings.serverUrl?.value.orEmpty()))
    val state: StateFlow<ConnectState> = _state.asStateFlow()

    fun onInput(value: String) = _state.update { it.copy(input = value, inputError = null, problem = null) }

    fun connect() {
        val url = when (val r = ServerUrl.parse(_state.value.input)) {
            is Result.Ok -> r.value
            is Result.Err -> return _state.update { it.copy(inputError = r.error) }
        }
        _state.update { it.copy(checking = true, problem = null) }
        viewModelScope.launch {
            when (val status = checker.check(url)) {
                is ServerStatus.Ready -> {
                    // A sign-in belongs to one server.
                    if (settings.serverUrl != url) tokens.clear()
                    settings.serverUrl = url
                    _state.update { it.copy(checking = false, connected = status) }
                }
                else -> _state.update { it.copy(checking = false, problem = status) }
            }
        }
    }
}
