package dev.voidrot.chorecast.core

import org.junit.Assert.assertEquals
import org.junit.Test

class CompatibilityTest {
    @Test
    fun comparesMajors() {
        assertEquals(Compatibility.Compatible, Compatibility.of(1, supported = 1))
        assertEquals(Compatibility.AppTooOld(2), Compatibility.of(2, supported = 1))
        assertEquals(Compatibility.ServerTooOld(1), Compatibility.of(1, supported = 2))
    }
}
