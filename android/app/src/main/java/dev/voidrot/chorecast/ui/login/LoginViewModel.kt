package dev.voidrot.chorecast.ui.login

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.voidrot.chorecast.core.LoginResult
import dev.voidrot.chorecast.core.ParentAuth
import dev.voidrot.chorecast.core.SecondFactor
import dev.voidrot.chorecast.core.ServerUrl
import dev.voidrot.chorecast.core.TokenStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

enum class LoginStep { Password, Code, Recovery }

/** Why the last attempt failed, for the screen to explain. */
sealed interface LoginError {
    data object InvalidCredentials : LoginError
    data object InvalidCode : LoginError
    data class RateLimited(val minutes: Long) : LoginError
    data class Unreachable(val reason: String) : LoginError
    data class Failed(val code: String) : LoginError
}

data class LoginState(
    val step: LoginStep = LoginStep.Password,
    val username: String = "",
    val password: String = "",
    val code: String = "",
    val submitting: Boolean = false,
    val error: LoginError? = null,
    /** Set once signed in, with the recovery codes left when one was used. */
    val signedIn: SignedIn? = null,
) {
    data class SignedIn(val userId: Long, val recoveryCodesLeft: Long?)

    val canSubmit: Boolean
        get() = !submitting && username.isNotBlank() && password.isNotEmpty() &&
            (step == LoginStep.Password || code.isNotBlank())
}

/** Parent sign-in: password, then an authenticator or recovery code when two-factor sign-in is on. */
class LoginViewModel(
    private val url: ServerUrl,
    private val auth: ParentAuth,
    private val tokens: TokenStore,
    private val deviceLabel: String,
) : ViewModel() {
    private val _state = MutableStateFlow(LoginState())
    val state: StateFlow<LoginState> = _state.asStateFlow()

    fun onUsername(v: String) = _state.update { it.copy(username = v, error = null) }
    fun onPassword(v: String) = _state.update { it.copy(password = v, error = null) }
    fun onCode(v: String) = _state.update { it.copy(code = v, error = null) }

    fun useRecoveryCode(on: Boolean) =
        _state.update { it.copy(step = if (on) LoginStep.Recovery else LoginStep.Code, code = "", error = null) }

    fun startOver() = _state.update { LoginState(username = it.username) }

    fun submit() {
        val s = _state.value
        if (!s.canSubmit) return
        val factor = when (s.step) {
            LoginStep.Password -> null
            LoginStep.Code -> SecondFactor.Code(s.code)
            LoginStep.Recovery -> SecondFactor.Recovery(s.code)
        }
        _state.update { it.copy(submitting = true, error = null) }
        viewModelScope.launch {
            val result = auth.login(url, s.username, s.password, deviceLabel, factor)
            _state.update { st ->
                val idle = st.copy(submitting = false)
                when (result) {
                    is LoginResult.Success -> {
                        tokens.write(result.token)
                        idle.copy(signedIn = LoginState.SignedIn(result.userId, result.recoveryCodesLeft))
                    }
                    LoginResult.SecondFactorRequired -> idle.copy(step = LoginStep.Code, code = "")
                    LoginResult.InvalidCredentials ->
                        idle.copy(step = LoginStep.Password, password = "", code = "", error = LoginError.InvalidCredentials)
                    LoginResult.InvalidCode -> idle.copy(error = LoginError.InvalidCode)
                    is LoginResult.RateLimited ->
                        idle.copy(error = LoginError.RateLimited(((result.retryAfterSeconds ?: 900) + 59) / 60))
                    is LoginResult.Unreachable -> idle.copy(error = LoginError.Unreachable(result.reason))
                    is LoginResult.Failed -> idle.copy(error = LoginError.Failed(result.code))
                }
            }
        }
    }
}
