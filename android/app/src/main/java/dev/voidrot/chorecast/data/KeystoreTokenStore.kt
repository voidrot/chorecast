package dev.voidrot.chorecast.data

import android.content.SharedPreferences
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import androidx.core.content.edit
import dev.voidrot.chorecast.core.TokenStore
import java.security.GeneralSecurityException
import java.security.KeyStore
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Keeps the API token encrypted at rest with AES-256-GCM. The key lives in the Android Keystore
 * and never leaves it; only the IV and ciphertext are written to SharedPreferences.
 */
class KeystoreTokenStore(private val prefs: SharedPreferences) : TokenStore {
    private val keyStore: KeyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }

    override fun read(): String? {
        val stored = prefs.getString(KEY_TOKEN, null) ?: return null
        return try {
            val parts = stored.split(':', limit = 2)
            require(parts.size == 2) { "malformed stored token" }
            val (iv, ciphertext) = parts.map { Base64.getDecoder().decode(it) }
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(TAG_BITS, iv))
            String(cipher.doFinal(ciphertext), Charsets.UTF_8)
        } catch (_: GeneralSecurityException) {
            clear() // key was lost (e.g. device restored); the device must be paired again
            null
        } catch (_: IllegalArgumentException) {
            clear() // corrupt value
            null
        }
    }

    override fun write(token: String) {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val ciphertext = cipher.doFinal(token.toByteArray(Charsets.UTF_8))
        val enc = Base64.getEncoder()
        prefs.edit { putString(KEY_TOKEN, enc.encodeToString(cipher.iv) + ":" + enc.encodeToString(ciphertext)) }
    }

    override fun clear() = prefs.edit { remove(KEY_TOKEN) }

    private fun key(): SecretKey =
        (keyStore.getEntry(KEY_ALIAS, null) as? KeyStore.SecretKeyEntry)?.secretKey ?: generateKey()

    private fun generateKey(): SecretKey = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE).run {
        init(
            KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        generateKey()
    }

    private companion object {
        const val ANDROID_KEYSTORE = "AndroidKeyStore"
        const val KEY_ALIAS = "chorecast_token_key"
        const val KEY_TOKEN = "api_token"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val TAG_BITS = 128
    }
}
