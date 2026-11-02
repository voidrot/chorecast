package dev.voidrot.chorecast.ui.device

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.api.models.Chore
import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.ChoreInstanceRejection
import dev.voidrot.chorecast.core.CompleteError
import dev.voidrot.chorecast.core.EvidenceRequirement
import dev.voidrot.chorecast.core.ChoreStatusView
import dev.voidrot.chorecast.core.TodayChore
import dev.voidrot.chorecast.core.requirements
import dev.voidrot.chorecast.core.slotTime

/**
 * One chore: when it's due, what to do (steps), what to hand in (evidence rules) and, after a
 * parent sent it back, why. [details] is null until the chore has been read; the instance's own
 * facts show meanwhile. The complete button starts the flow in [CompleteForm].
 */
@Composable
fun ChoreDetailScreen(
    chore: TodayChore,
    details: Chore?,
    detailsFailed: Boolean,
    completeError: CompleteError?,
    onComplete: () -> Unit,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val instance = chore.instance
    Column(modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
            Text(instance.choreName, style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
            TextButton(onClick = onBack) { Text(stringResource(R.string.action_back)) }
        }
        Column(Modifier.weight(1f).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                if (!instance.planned) StatusLabel(chore.status)
                Text(dueText(instance), style = MaterialTheme.typography.bodyLarge)
                chore.points?.let { PointsLabel(it) }
            }
            if (chore.status == ChoreStatusView.REDO) instance.lastRejection?.let { RejectionCard(it) }
            details?.description?.takeIf { it.isNotBlank() }?.let { Text(it) }
            details?.steps?.takeIf { it.isNotEmpty() }?.let { steps ->
                Section(stringResource(R.string.chore_steps)) {
                    steps.forEachIndexed { i, step -> Text("${i + 1}. $step") }
                }
            }
            details?.evidence?.requirements()?.takeIf { it.isNotEmpty() }?.let { needs ->
                Section(stringResource(R.string.chore_evidence)) {
                    needs.forEach { Text("• " + requirementText(it)) }
                }
            }
            if (details == null && detailsFailed) {
                Text(stringResource(R.string.chore_loading_failed), color = MaterialTheme.colorScheme.error)
            }
        }
        completeError?.let { Text(completeErrorText(it), color = MaterialTheme.colorScheme.error) }
        // The evidence rules come with the chore, so the complete flow waits for them.
        if (chore.canComplete) {
            Button(onClick = onComplete, enabled = details != null, modifier = Modifier.fillMaxWidth()) {
                Text(stringResource(R.string.chore_complete))
            }
        }
    }
}

@Composable
private fun Section(title: String, content: @Composable () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(title, style = MaterialTheme.typography.titleMedium)
        content()
    }
}

@Composable
private fun RejectionCard(rejection: ChoreInstanceRejection) {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer), modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(stringResource(R.string.chore_rejected_title), style = MaterialTheme.typography.titleSmall)
            rejection.reason?.let { Text(rejectionReasonText(it)) }
            rejection.text?.takeIf { it.isNotBlank() }?.let { Text(it) }
        }
    }
}

@Composable
private fun rejectionReasonText(r: ChoreInstanceRejection.Reason): String = stringResource(
    when (r) {
        ChoreInstanceRejection.Reason.NOT_DONE -> R.string.chore_reject_not_done
        ChoreInstanceRejection.Reason.NOT_FINISHED -> R.string.chore_reject_not_finished
        ChoreInstanceRejection.Reason.NEEDS_REDO -> R.string.chore_reject_needs_redo
        ChoreInstanceRejection.Reason.EVIDENCE_UNCLEAR -> R.string.chore_reject_evidence_unclear
    },
)

@Composable
private fun requirementText(r: EvidenceRequirement): String = when (r) {
    is EvidenceRequirement.Photos -> {
        val photos = pluralStringResource(R.plurals.chore_evidence_photos, r.count, r.count)
        if (r.beforeAfter) photos + ". " + stringResource(R.string.chore_evidence_before_after) else photos
    }
    EvidenceRequirement.Checklist -> stringResource(R.string.chore_evidence_checklist)
    EvidenceRequirement.Note -> stringResource(R.string.chore_evidence_note)
    EvidenceRequirement.Video -> stringResource(R.string.chore_evidence_video)
}

@Composable
private fun dueText(i: ChoreInstance): String {
    val time = slotTime(i.slot)
    return when {
        i.planned -> stringResource(R.string.kid_due_planned, formatDate(i.date))
        time != null -> stringResource(R.string.kid_due_at, formatTime(time))
        i.flexible == true -> stringResource(R.string.kid_due_week_close)
        else -> stringResource(R.string.kid_due_day_close)
    }
}
