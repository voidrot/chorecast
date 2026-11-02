package dev.voidrot.chorecast.core

import com.google.zxing.BinaryBitmap
import com.google.zxing.LuminanceSource
import com.google.zxing.common.HybridBinarizer
import com.google.zxing.qrcode.QRCodeReader
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant

class QrCodeTest {
    private val url = (ServerUrl.parse("http://192.168.1.20:8080") as Result.Ok).value

    /** Reads the matrix back the way a scanner would: dark modules are 0, light are 255, scaled up. */
    private fun decode(m: QrMatrix): String {
        val scale = 4
        val n = m.size * scale
        val source = object : LuminanceSource(n, n) {
            private fun lum(x: Int, y: Int): Byte = if (m[x / scale, y / scale]) 0 else 255.toByte()
            override fun getRow(y: Int, row: ByteArray?): ByteArray = (row ?: ByteArray(n)).also { r -> for (x in 0 until n) r[x] = lum(x, y) }
            override fun getMatrix(): ByteArray = ByteArray(n * n) { lum(it % n, it / n) }
            override fun isCropSupported() = false
        }
        return QRCodeReader().decode(BinaryBitmap(HybridBinarizer(source))).text
    }

    @Test
    fun pairingPayloadRoundTripsThroughTheParserAndTheQrCode() {
        val qr = PairingQr(url, "k7mp-q2xd")
        assertEquals(qr, PairingQr.parse(qr.toPayload()))
        assertEquals(qr, PairingQr.parse(decode(QrMatrix.encode(qr.toPayload()))))
    }

    @Test
    fun matrixGrowsWithContent() {
        assertTrue(QrMatrix.encode("x".repeat(400)).size > QrMatrix.encode("x").size)
    }

    @Test
    fun countdownNeverGoesNegative() {
        val exp = Instant.parse("2026-11-02T10:05:00Z")
        assertEquals(300, secondsUntil(exp, Instant.parse("2026-11-02T10:00:00Z")))
        assertEquals(0, secondsUntil(exp, Instant.parse("2026-11-02T10:06:00Z")))
        assertEquals("4:05", formatCountdown(245))
    }
}
