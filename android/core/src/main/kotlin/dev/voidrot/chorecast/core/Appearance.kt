package dev.voidrot.chorecast.core

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** The app's theme choice. The names are persisted: never rename an entry. */
enum class ThemeMode {
    SYSTEM,
    LIGHT,
    DARK;

    companion object {
        /** A stored value back to a mode; missing or unknown values follow the system. */
        fun fromStored(value: String?): ThemeMode = entries.firstOrNull { it.name == value } ?: SYSTEM
    }
}

/** Appearance on this device. [dynamicColor] only has an effect on Android 12+. */
data class Appearance(val theme: ThemeMode = ThemeMode.SYSTEM, val dynamicColor: Boolean = true) {
    fun darkTheme(systemDark: Boolean): Boolean = when (theme) {
        ThemeMode.SYSTEM -> systemDark
        ThemeMode.LIGHT -> false
        ThemeMode.DARK -> true
    }
}

interface AppearanceStore {
    fun load(): Appearance
    fun save(appearance: Appearance)
}

class InMemoryAppearanceStore(var saved: Appearance = Appearance()) : AppearanceStore {
    override fun load(): Appearance = saved
    override fun save(appearance: Appearance) {
        saved = appearance
    }
}

/** App-scoped appearance. Call the setters from the main thread. */
class AppearanceSettings(private val store: AppearanceStore) {
    private val _state = MutableStateFlow(store.load())
    val state: StateFlow<Appearance> = _state.asStateFlow()

    fun setTheme(mode: ThemeMode) = update { it.copy(theme = mode) }

    fun setDynamicColor(enabled: Boolean) = update { it.copy(dynamicColor = enabled) }

    private fun update(change: (Appearance) -> Appearance) {
        val next = change(_state.value)
        if (next != _state.value) {
            _state.value = next
            store.save(next)
        }
    }
}
