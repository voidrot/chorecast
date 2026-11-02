package dev.voidrot.chorecast.ui.device

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.api.models.KidProfile
import dev.voidrot.chorecast.core.AvatarPhotos
import dev.voidrot.chorecast.core.bundledAvatarNumber
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** The bundled avatars, `pixel-01` first (rendered from web/public/avatars by web/scripts/gen-android-avatars.sh). */
private val bundledAvatars = intArrayOf(
    R.drawable.avatar_pixel_01, R.drawable.avatar_pixel_02, R.drawable.avatar_pixel_03, R.drawable.avatar_pixel_04,
    R.drawable.avatar_pixel_05, R.drawable.avatar_pixel_06, R.drawable.avatar_pixel_07, R.drawable.avatar_pixel_08,
    R.drawable.avatar_pixel_09, R.drawable.avatar_pixel_10, R.drawable.avatar_pixel_11, R.drawable.avatar_pixel_12,
    R.drawable.avatar_pixel_13, R.drawable.avatar_pixel_14, R.drawable.avatar_pixel_15, R.drawable.avatar_pixel_16,
    R.drawable.avatar_pixel_17, R.drawable.avatar_pixel_18, R.drawable.avatar_pixel_19, R.drawable.avatar_pixel_20,
    R.drawable.avatar_pixel_21, R.drawable.avatar_pixel_22, R.drawable.avatar_pixel_23, R.drawable.avatar_pixel_24,
)

/**
 * A kid's picture: their uploaded photo if the picker carries a signed link to one, else the bundled
 * avatar, else a colored circle with their initials. The photo shows once downloaded; a failed or
 * expired download leaves what is already showing.
 */
@Composable
internal fun KidAvatar(kid: KidProfile, photos: AvatarPhotos, sizeDp: Int) {
    var photo by remember(kid.id) { mutableStateOf<ImageBitmap?>(null) }
    val url = kid.avatarPhotoUrl
    LaunchedEffect(kid.id, url) {
        if (url == null) {
            photo = null
            return@LaunchedEffect
        }
        val bytes = photos.fetch(url) ?: return@LaunchedEffect
        val decoded = withContext(Dispatchers.Default) { BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.asImageBitmap() }
        if (decoded != null) photo = decoded
    }

    val shown = photo
    val bundled = bundledAvatarNumber(kid.avatar)?.let { bundledAvatars[it - 1] }
    val modifier = Modifier.size(sizeDp.dp).clip(CircleShape)
    when {
        shown != null -> Image(shown, contentDescription = null, modifier = modifier, contentScale = ContentScale.Crop)
        bundled != null -> Image(painterResource(bundled), contentDescription = null, modifier = modifier, contentScale = ContentScale.Crop)
        else -> Initials(kid.displayName, kid.color, sizeDp)
    }
}

/** A colored circle with the kid's initials. */
@Composable
private fun Initials(name: String, color: String, sizeDp: Int) {
    val bg = runCatching { Color(android.graphics.Color.parseColor(color)) }.getOrDefault(MaterialTheme.colorScheme.primary)
    val text = name.trim().split(Regex("\\s+")).take(2).mapNotNull { it.firstOrNull()?.uppercaseChar() }.joinToString("")
    Box(Modifier.size(sizeDp.dp).clip(CircleShape).background(bg), contentAlignment = Alignment.Center) {
        Text(text, color = Color.White, fontSize = (sizeDp / 2.6).sp, style = MaterialTheme.typography.titleLarge)
    }
}
