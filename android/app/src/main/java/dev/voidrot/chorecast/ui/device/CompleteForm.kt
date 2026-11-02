package dev.voidrot.chorecast.ui.device

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.api.models.EvidencePhotoInput
import dev.voidrot.chorecast.core.CompleteError
import dev.voidrot.chorecast.core.CompletionDraft
import dev.voidrot.chorecast.core.CompletionNeeds
import dev.voidrot.chorecast.core.MAX_NOTE_LENGTH
import dev.voidrot.chorecast.core.MAX_PHOTOS
import dev.voidrot.chorecast.core.PhotoProblem

/**
 * The evidence for one chore: the checklist, a note and the photos, and the button that sends
 * them. The button stays off until [needs] says nothing is missing.
 */
@Composable
fun CompleteForm(
    choreName: String,
    needs: CompletionNeeds,
    draft: CompletionDraft,
    submitting: Boolean,
    error: CompleteError?,
    onToggleStep: (Int) -> Unit,
    onNote: (String) -> Unit,
    onTakePhoto: () -> Unit,
    onRemovePhoto: (String) -> Unit,
    onSubmit: () -> Unit,
    onCancel: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val missing = needs.missing(draft)
    Column(modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Text(choreName, style = MaterialTheme.typography.headlineSmall)
        Column(
            Modifier.weight(1f).verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            if (needs.steps.isNotEmpty()) {
                Text(stringResource(R.string.chore_steps), style = MaterialTheme.typography.titleMedium)
                needs.steps.forEachIndexed { i, step ->
                    Row(
                        Modifier.fillMaxWidth().toggleable(value = i in draft.ticked, role = Role.Checkbox, onValueChange = { onToggleStep(i) }),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Checkbox(checked = i in draft.ticked, onCheckedChange = null)
                        Text(step, modifier = Modifier.padding(start = 8.dp))
                    }
                }
            }
            OutlinedTextField(
                value = draft.note,
                onValueChange = onNote,
                label = { Text(stringResource(if (needs.note) R.string.complete_note_required else R.string.complete_note_optional)) },
                supportingText = { Text("${draft.note.length}/$MAX_NOTE_LENGTH") },
                modifier = Modifier.fillMaxWidth(),
            )
            if (needs.photosRequired > 0 || draft.photos.isNotEmpty()) {
                Text(stringResource(R.string.complete_photos), style = MaterialTheme.typography.titleMedium)
                draft.photos.forEachIndexed { i, photo ->
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                        Text(photoLabel(i, needs.roleOf(i)))
                        TextButton(onClick = { onRemovePhoto(photo.captureId) }, enabled = !submitting) {
                            Text(stringResource(R.string.complete_remove_photo))
                        }
                    }
                }
                if (draft.photos.size < MAX_PHOTOS) {
                    OutlinedButton(onClick = onTakePhoto, enabled = !submitting, modifier = Modifier.fillMaxWidth()) {
                        Text(stringResource(R.string.complete_take_photo))
                    }
                }
            }
            missing.forEach { Text(missingText(it), style = MaterialTheme.typography.bodyMedium) }
            error?.let { Text(completeErrorText(it), color = MaterialTheme.colorScheme.error) }
        }
        Button(onClick = onSubmit, enabled = missing.isEmpty() && !submitting, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(if (submitting) R.string.complete_sending else R.string.complete_submit))
        }
        TextButton(onClick = onCancel, enabled = !submitting) { Text(stringResource(R.string.action_back)) }
    }
}

@Composable
private fun photoLabel(index: Int, role: EvidencePhotoInput.Role?): String = when (role) {
    EvidencePhotoInput.Role.BEFORE -> stringResource(R.string.complete_photo_before)
    EvidencePhotoInput.Role.AFTER -> stringResource(R.string.complete_photo_after)
    null -> stringResource(R.string.complete_photo_n, index + 1)
}

@Composable
private fun missingText(m: CompletionNeeds.Missing): String = when (m) {
    is CompletionNeeds.Missing.Photos -> pluralStringResource(R.plurals.complete_missing_photos, m.more, m.more)
    CompletionNeeds.Missing.Checklist -> stringResource(R.string.complete_missing_checklist)
    CompletionNeeds.Missing.Note -> stringResource(R.string.complete_missing_note)
    CompletionNeeds.Missing.Video -> stringResource(R.string.complete_video_unsupported)
}

/** What to tell the kid about a failed submit. */
@Composable
fun completeErrorText(e: CompleteError): String = stringResource(
    when (e) {
        is CompleteError.Offline -> R.string.complete_error_offline
        CompleteError.SignedOut -> R.string.complete_error_signed_out
        CompleteError.TooManyPhotos -> R.string.complete_error_too_many_photos
        is CompleteError.BadPhoto -> when (e.reason) {
            PhotoProblem.DUPLICATE -> R.string.complete_error_photo_duplicate
            PhotoProblem.TOO_LARGE -> R.string.complete_error_photo_too_large
            PhotoProblem.UNSUPPORTED, PhotoProblem.LOST -> R.string.complete_error_photo_unusable
        }
        is CompleteError.Invalid -> R.string.complete_error_invalid
        CompleteError.VideoUnsupported -> R.string.complete_video_unsupported
        CompleteError.Closed -> R.string.complete_error_closed
        CompleteError.NotOpen -> R.string.complete_error_not_open
        is CompleteError.Failed -> R.string.complete_error_failed
    },
)
