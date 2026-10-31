package dev.voidrot.chorecast.ui.startup

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.core.ServerChecker
import dev.voidrot.chorecast.core.ServerStatus
import dev.voidrot.chorecast.data.SettingsStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface StartupState {
    data object Checking : StartupState
    data object NeedsServer : StartupState
    data class Ready(val status: ServerStatus.Ready) : StartupState
    data class Problem(val status: ServerStatus) : StartupState
}

/** On launch: is a server configured, and is it reachable and compatible? */
class StartupViewModel(
    private val settings: SettingsStore,
    private val checker: ServerChecker,
) : ViewModel() {
    private val _state = MutableStateFlow<StartupState>(StartupState.Checking)
    val state: StateFlow<StartupState> = _state.asStateFlow()

    init {
        check()
    }

    fun check() {
        val url = settings.serverUrl
        if (url == null) {
            _state.value = StartupState.NeedsServer
            return
        }
        _state.value = StartupState.Checking
        viewModelScope.launch {
            _state.value = when (val status = checker.check(url)) {
                is ServerStatus.Ready -> StartupState.Ready(status)
                else -> StartupState.Problem(status)
            }
        }
    }
}
