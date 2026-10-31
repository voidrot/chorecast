package dev.voidrot.chorecast.ui.startup

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.core.ParentAuth
import dev.voidrot.chorecast.core.ServerChecker
import dev.voidrot.chorecast.core.ServerStatus
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.SessionCheck
import dev.voidrot.chorecast.core.TokenStore
import dev.voidrot.chorecast.data.SettingsStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface StartupState {
    data object Checking : StartupState
    data object NeedsServer : StartupState
    data class NeedsLogin(val url: ServerUrl) : StartupState
    data class SignedIn(val url: ServerUrl) : StartupState
    data class Problem(val status: ServerStatus) : StartupState
}

/** On launch: is a server configured and compatible, and is the saved sign-in still valid? */
class StartupViewModel(
    private val settings: SettingsStore,
    private val checker: ServerChecker,
    private val tokens: TokenStore,
    private val auth: ParentAuth,
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
            val status = checker.check(url)
            if (status !is ServerStatus.Ready) {
                _state.value = StartupState.Problem(status)
                return@launch
            }
            val token = tokens.read()
            if (token == null) {
                _state.value = StartupState.NeedsLogin(url)
                return@launch
            }
            _state.value = when (val s = auth.check(url, token)) {
                is SessionCheck.SignedIn -> StartupState.SignedIn(url)
                SessionCheck.SignedOut -> {
                    tokens.clear()
                    StartupState.NeedsLogin(url)
                }
                is SessionCheck.Unreachable -> StartupState.Problem(ServerStatus.Unreachable(url, s.reason))
            }
        }
    }
}
