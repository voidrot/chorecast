package dev.voidrot.chorecast

import org.junit.Assert.assertEquals
import org.junit.Test

class SmokeTest {
    @Test
    fun applicationIdIsStable() {
        assertEquals("dev.voidrot.chorecast", BuildConfig.APPLICATION_ID.removeSuffix(".debug"))
    }
}
