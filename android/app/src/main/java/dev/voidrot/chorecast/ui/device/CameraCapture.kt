package dev.voidrot.chorecast.ui.device

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.ImageCapture
import androidx.camera.core.ImageCaptureException
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.compose.LocalLifecycleOwner
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.CapturedPhoto
import dev.voidrot.chorecast.core.newCaptureId
import java.io.File
import java.time.Instant

/**
 * In-app camera: the only way to add a photo (no gallery picker). The capture time and the capture
 * id are taken when the shutter is pressed, before the file is written. Asks for the camera
 * permission when it opens; if it is refused the kid can ask again or go back.
 */
@Composable
fun CameraCapture(
    newFile: (captureId: String) -> File,
    onCaptured: (CapturedPhoto) -> Unit,
    onFailed: (captureId: String) -> Unit,
    onCancel: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    var granted by remember {
        mutableStateOf(ContextCompat.checkSelfPermission(context, Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED)
    }
    var asked by remember { mutableStateOf(false) }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {
        granted = it
        asked = true
    }
    if (!granted) {
        LaunchedEffect(Unit) { if (!asked) permission.launch(Manifest.permission.CAMERA) }
        Column(modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text(stringResource(R.string.camera_permission_needed))
            Button(onClick = { permission.launch(Manifest.permission.CAMERA) }, modifier = Modifier.fillMaxWidth()) {
                Text(stringResource(R.string.camera_permission_grant))
            }
            TextButton(onClick = onCancel) { Text(stringResource(R.string.action_cancel)) }
        }
        return
    }

    val lifecycleOwner = LocalLifecycleOwner.current
    val providerFuture = remember { ProcessCameraProvider.getInstance(context) }
    val imageCapture = remember {
        ImageCapture.Builder().setCaptureMode(ImageCapture.CAPTURE_MODE_MINIMIZE_LATENCY).setJpegQuality(85).build()
    }
    val latestCaptured = rememberUpdatedState(onCaptured)
    val latestFailed = rememberUpdatedState(onFailed)
    var taking by remember { mutableStateOf(false) }

    DisposableEffect(Unit) {
        onDispose { if (providerFuture.isDone) providerFuture.get().unbindAll() }
    }
    Column(modifier.fillMaxSize().padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        AndroidView(
            factory = { ctx ->
                val view = PreviewView(ctx)
                providerFuture.addListener({
                    val preview = Preview.Builder().build().also { it.setSurfaceProvider(view.surfaceProvider) }
                    val provider = providerFuture.get()
                    provider.unbindAll()
                    provider.bindToLifecycle(lifecycleOwner, CameraSelector.DEFAULT_BACK_CAMERA, preview, imageCapture)
                }, ContextCompat.getMainExecutor(ctx))
                view
            },
            modifier = Modifier.weight(1f).fillMaxWidth(),
        )
        Button(
            enabled = !taking,
            onClick = {
                taking = true
                // The capture time and id are fixed here, at the shutter.
                val captureId = newCaptureId()
                val capturedAt = Instant.now()
                val options = ImageCapture.OutputFileOptions.Builder(newFile(captureId)).build()
                imageCapture.takePicture(
                    options,
                    ContextCompat.getMainExecutor(context),
                    object : ImageCapture.OnImageSavedCallback {
                        override fun onImageSaved(output: ImageCapture.OutputFileResults) {
                            taking = false
                            latestCaptured.value(CapturedPhoto(captureId, capturedAt))
                        }

                        override fun onError(exception: ImageCaptureException) {
                            taking = false
                            latestFailed.value(captureId)
                        }
                    },
                )
            },
            modifier = Modifier.fillMaxWidth(),
        ) { Text(stringResource(R.string.camera_take_photo)) }
        TextButton(onClick = onCancel) { Text(stringResource(R.string.action_cancel)) }
    }
}
