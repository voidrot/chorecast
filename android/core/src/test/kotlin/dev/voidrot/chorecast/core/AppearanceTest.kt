package dev.voidrot.chorecast.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class AppearanceTest {

    @Test
    fun storedNamesAreStable() {
        assertEquals(listOf("SYSTEM", "LIGHT", "DARK"), ThemeMode.entries.map { it.name })
    }

    @Test
    fun fromStoredFallsBackToSystem() {
        assertEquals(ThemeMode.SYSTEM, ThemeMode.fromStored(null))
        assertEquals(ThemeMode.SYSTEM, ThemeMode.fromStored(""))
        assertEquals(ThemeMode.SYSTEM, ThemeMode.fromStored("dark"))
        assertEquals(ThemeMode.SYSTEM, ThemeMode.fromStored("SEPIA"))
        assertEquals(ThemeMode.LIGHT, ThemeMode.fromStored("LIGHT"))
        assertEquals(ThemeMode.DARK, ThemeMode.fromStored("DARK"))
    }

    @Test
    fun defaultsAreSystemWithDynamicColor() {
        val defaults = Appearance()
        assertEquals(ThemeMode.SYSTEM, defaults.theme)
        assertTrue(defaults.dynamicColor)
    }

    @Test
    fun darkThemeFollowsSystemOnlyForSystem() {
        assertTrue(Appearance(ThemeMode.SYSTEM).darkTheme(systemDark = true))
        assertFalse(Appearance(ThemeMode.SYSTEM).darkTheme(systemDark = false))
        assertFalse(Appearance(ThemeMode.LIGHT).darkTheme(systemDark = true))
        assertFalse(Appearance(ThemeMode.LIGHT).darkTheme(systemDark = false))
        assertTrue(Appearance(ThemeMode.DARK).darkTheme(systemDark = true))
        assertTrue(Appearance(ThemeMode.DARK).darkTheme(systemDark = false))
    }

    @Test
    fun startsFromSavedAppearance() {
        val settings = AppearanceSettings(InMemoryAppearanceStore(Appearance(ThemeMode.DARK, false)))
        assertEquals(Appearance(ThemeMode.DARK, false), settings.state.value)
    }

    @Test
    fun setThemePublishesAndSaves() {
        val store = InMemoryAppearanceStore(Appearance(dynamicColor = false))
        val settings = AppearanceSettings(store)

        settings.setTheme(ThemeMode.LIGHT)

        assertEquals(ThemeMode.LIGHT, settings.state.value.theme)
        assertEquals(settings.state.value, store.saved)
        assertFalse(settings.state.value.dynamicColor)
    }

    @Test
    fun setDynamicColorKeepsTheme() {
        val store = InMemoryAppearanceStore(Appearance(ThemeMode.DARK, dynamicColor = true))
        val settings = AppearanceSettings(store)

        settings.setDynamicColor(false)

        assertEquals(Appearance(ThemeMode.DARK, false), store.saved)
    }
}
