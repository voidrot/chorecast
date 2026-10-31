package dev.voidrot.chorecast.core

/**
 * Stores the device's API token. The Android implementation encrypts it with a key held in the
 * Android Keystore; tests use [InMemoryTokenStore].
 */
interface TokenStore {
    fun read(): String?
    fun write(token: String)
    fun clear()
}

class InMemoryTokenStore(private var token: String? = null) : TokenStore {
    override fun read(): String? = token
    override fun write(token: String) {
        this.token = token
    }
    override fun clear() {
        token = null
    }
}
