package dev.voidrot.chorecast.ui.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.core.HouseholdInfo
import dev.voidrot.chorecast.core.ParentAuth
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.SessionCheck
import dev.voidrot.chorecast.core.TokenStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** What the home screen shows about the household. */
data class HouseholdSummary(val name: String, val timezone: String, val dayEnd: String)

data class HomeState(
    val displayName: String? = null,
    val household: HouseholdSummary? = null,
    val totpEnabled: Boolean = false,
    /** True once signed out (by the user, or because the token stopped working). */
    val signedOut: Boolean = false,
)

class HomeViewModel(
    private val url: ServerUrl,
    private val auth: ParentAuth,
    private val tokens: TokenStore,
    private val household: HouseholdInfo,
) : ViewModel() {
    private val _state = MutableStateFlow(HomeState())
    val state: StateFlow<HomeState> = _state.asStateFlow()

    init {
        val token = tokens.read()
        if (token == null) {
            _state.update { it.copy(signedOut = true) }
        } else {
            viewModelScope.launch {
                when (val s = auth.check(url, token)) {
                    is SessionCheck.SignedIn -> {
                        _state.update { it.copy(displayName = s.me.displayName, totpEnabled = s.me.totpEnabled) }
                        household.get(url, token)?.let { h ->
                            _state.update { it.copy(household = HouseholdSummary(h.name, h.timezone, h.dayEnd)) }
                        }
                    }
                    SessionCheck.SignedOut -> {
                        tokens.clear()
                        _state.update { it.copy(signedOut = true) }
                    }
                    is SessionCheck.Unreachable -> Unit // keep showing home; the next screen will retry
                }
            }
        }
    }

    fun signOut() {
        val token = tokens.read()
        tokens.clear()
        viewModelScope.launch {
            if (token != null) auth.logout(url, token)
            _state.update { it.copy(signedOut = true) }
        }
    }
}
