package dev.voidrot.chorecast.data

import dev.voidrot.chorecast.core.CaptureStore
import java.io.File

/**
 * Captured photos as files in the app's private storage, named by capture id. They stay until the
 * completion is accepted (or the kid removes them), so a retry after a lost connection can send
 * the same file again.
 */
class FileCaptureStore(private val dir: File) : CaptureStore {
    /** Where the camera writes the photo with [captureId]. */
    fun fileFor(captureId: String): File {
        dir.mkdirs()
        return File(dir, "$captureId.jpg")
    }

    override fun read(captureId: String): ByteArray? =
        fileFor(captureId).takeIf { it.isFile }?.readBytes()

    override fun delete(captureId: String) {
        fileFor(captureId).delete()
    }
}
