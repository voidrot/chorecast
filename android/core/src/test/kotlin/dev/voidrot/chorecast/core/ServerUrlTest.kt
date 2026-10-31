package dev.voidrot.chorecast.core

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ServerUrlTest {
    private fun ok(input: String): ServerUrl = when (val r = ServerUrl.parse(input)) {
        is Result.Ok -> r.value
        is Result.Err -> throw AssertionError("parse($input) failed: ${r.error}")
    }

    private fun err(input: String): ServerUrl.Error = when (val r = ServerUrl.parse(input)) {
        is Result.Ok -> throw AssertionError("parse($input) succeeded: ${r.value}")
        is Result.Err -> r.error
    }

    @Test
    fun normalizes() {
        val cases = mapOf(
            "chores.example.com" to "https://chores.example.com",
            "  HTTPS://Chores.Example.com/  " to "https://chores.example.com",
            "http://192.168.1.20:8080" to "http://192.168.1.20:8080",
            "https://home.example.com/chorecast/" to "https://home.example.com/chorecast",
            "https://chores.example.com/api/v1" to "https://chores.example.com",
            "https://chores.example.com/api/v1/" to "https://chores.example.com",
        )
        for ((input, want) in cases) assertEquals(input, want, ok(input).value)
    }

    @Test
    fun apiBaseAndCleartext() {
        assertEquals("https://a.example/sub/api/v1", ok("https://a.example/sub").apiBase)
        assertTrue(ok("http://10.0.0.2:8080").isCleartext)
        assertFalse(ok("a.example").isCleartext)
    }

    @Test
    fun rejects() {
        assertEquals(ServerUrl.Error.Empty, err("   "))
        assertEquals(ServerUrl.Error.UnsupportedScheme, err("ftp://a.example"))
        for (bad in listOf("https://", "https://user:pw@a.example", "https://a.example/?x=1", "https://a.example/#x", "http://bad host")) {
            assertEquals(bad, ServerUrl.Error.Invalid, err(bad))
        }
    }
}
