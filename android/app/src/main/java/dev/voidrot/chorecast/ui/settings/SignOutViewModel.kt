package dev.voidrot.chorecast.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.core.ParentAuth
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.TokenStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** Signs the parent out from Settings; [signedOut] turns true once the token is cleared and the server told. */
class SignOutViewModel(
    private val url: ServerUrl,
    private val auth: ParentAuth,
    private val tokens: TokenStore,
) : ViewModel() {
    private val _signedOut = MutableStateFlow(false)
    val signedOut: StateFlow<Boolean> = _signedOut.asStateFlow()

    fun signOut() {
        val token = tokens.read()
        tokens.clear()
        viewModelScope.launch {
            if (token != null) auth.logout(url, token)
            _signedOut.value = true
        }
    }
}
