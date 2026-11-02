package dev.voidrot.chorecast.core

import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.qrcode.QRCodeWriter
import com.google.zxing.qrcode.decoder.ErrorCorrectionLevel
import java.time.Duration
import java.time.Instant
import java.util.Locale

/** A square QR code: [size] modules per side, quiet zone included. Dark modules are true. */
class QrMatrix(val size: Int, private val dark: BooleanArray) {
    operator fun get(x: Int, y: Int): Boolean = dark[y * size + x]

    companion object {
        /** Encodes [content]; the matrix grows with the content. Throws if it can't fit in a QR code. */
        fun encode(content: String): QrMatrix {
            val hints = mapOf(
                EncodeHintType.ERROR_CORRECTION to ErrorCorrectionLevel.M,
                EncodeHintType.CHARACTER_SET to "UTF-8",
                EncodeHintType.MARGIN to 4,
            )
            // Asking for 0x0 yields the smallest matrix, one pixel per module, with the quiet zone.
            val m = QRCodeWriter().encode(content, BarcodeFormat.QR_CODE, 0, 0, hints)
            val dark = BooleanArray(m.width * m.height) { m[it % m.width, it / m.width] }
            return QrMatrix(m.width, dark)
        }
    }
}

/** Whole seconds until [expiresAt], never negative. */
fun secondsUntil(expiresAt: Instant, now: Instant): Long =
    Duration.between(now, expiresAt).seconds.coerceAtLeast(0)

/** `m:ss` for a countdown. */
fun formatCountdown(seconds: Long): String = String.format(Locale.ROOT, "%d:%02d", seconds / 60, seconds % 60)
