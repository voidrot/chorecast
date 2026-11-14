package dev.voidrot.chorecast

import androidx.compose.material3.ColorScheme
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import dev.voidrot.chorecast.ui.theme.ChorecastDarkColors
import dev.voidrot.chorecast.ui.theme.ChorecastLightColors
import org.junit.Assert.assertTrue
import org.junit.Test

class ContrastTest {
    private fun ratio(a: Color, b: Color): Double {
        val l1 = a.luminance().toDouble()
        val l2 = b.luminance().toDouble()
        return (maxOf(l1, l2) + 0.05) / (minOf(l1, l2) + 0.05)
    }

    private fun check(name: String, scheme: ColorScheme) {
        val text = ratio(scheme.onPrimary, scheme.primary)
        assertTrue("$name onPrimary/primary $text", text >= 4.5)
        val body = ratio(scheme.onSurface, scheme.surface)
        assertTrue("$name onSurface/surface $body", body >= 4.5)
        val border = ratio(scheme.outline, scheme.surface)
        assertTrue("$name outline/surface $border", border >= 3.0)
    }

    @Test
    fun lightPaletteMeetsWcagContrast() = check("light", ChorecastLightColors)

    @Test
    fun darkPaletteMeetsWcagContrast() = check("dark", ChorecastDarkColors)
}
