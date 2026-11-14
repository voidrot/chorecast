package dev.voidrot.chorecast.ui.device

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModelStoreOwner
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.ActiveSession
import dev.voidrot.chorecast.core.AppearanceSettings
import dev.voidrot.chorecast.core.AvatarPhotos
import dev.voidrot.chorecast.core.PinError
import dev.voidrot.chorecast.ui.inbox.InboxScreen
import dev.voidrot.chorecast.ui.inbox.InboxViewModel
import kotlinx.coroutines.delay
import java.time.Instant

@Composable
fun DeviceScreen(
    viewModel: DeviceViewModel,
    todayViewModel: (KidAccess) -> TodayViewModel,
    parentModels: ParentModels,
    avatarPhotos: AvatarPhotos,
    appearanceSettings: AppearanceSettings,
    inboxViewModel: (KidAccess) -> InboxViewModel,
    onUnpaired: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val appearance by appearanceSettings.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.unpaired) { if (state.unpaired) onUnpaired() }

    Box(
        modifier.fillMaxSize().pointerInput(Unit) {
            // Any touch counts as activity for the kid session's auto-lock.
            awaitPointerEventScope {
                while (true) {
                    awaitPointerEvent(PointerEventPass.Initial)
                    viewModel.onInteraction()
                }
            }
        },
    ) {
        val active = state.active
        val picker = state.picker
        val ownKid = state.ownKid
        when {
            state.loading -> Centered { CircularProgressIndicator() }
            state.offline != null -> Centered {
                Text(stringResource(R.string.device_offline, state.offline.orEmpty()))
                Button(onClick = viewModel::load) { Text(stringResource(R.string.action_retry)) }
            }
            active is ActiveSession.Kid -> {
                val access = KidAccess(active.kid.id, active.kid.displayName, active.token)
                KidHome(
                    access,
                    viewModel.kidStore(access),
                    todayViewModel,
                    inboxViewModel,
                    onLock = viewModel::lock,
                    onParent = null,
                    onUnauthorized = viewModel::sessionRefused,
                )
            }
            active is ActiveSession.Parent && state.parentStore != null -> ParentMode(
                session = active,
                page = state.parentPage,
                store = state.parentStore!!,
                models = parentModels,
                appearance = appearance,
                onTheme = appearanceSettings::setTheme,
                onDynamicColor = appearanceSettings::setDynamicColor,
                onPage = viewModel::openParentPage,
                onLock = viewModel::lock,
                // The server refused the elevated token (expired or locked remotely): back to the picker.
                onRefused = viewModel::sessionRefused,
            )
            ownKid != null -> KidHome(
                ownKid,
                viewModel.kidStore(ownKid),
                todayViewModel,
                inboxViewModel,
                onLock = null,
                onParent = if (picker?.parents.isNullOrEmpty()) null else ({ viewModel.chooseParent(true) }),
                // Checks the device token with the picker once, which unpairs the device if it was revoked.
                onUnauthorized = viewModel::deviceTokenRefused,
            )
            picker != null -> Column(Modifier.fillMaxSize().padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                Text(stringResource(R.string.device_who), style = MaterialTheme.typography.headlineSmall)
                LazyVerticalGrid(
                    columns = GridCells.Adaptive(140.dp),
                    horizontalArrangement = Arrangement.spacedBy(16.dp),
                    verticalArrangement = Arrangement.spacedBy(16.dp),
                    modifier = Modifier.weight(1f),
                ) {
                    items(picker.kids, key = { it.id }) { kid ->
                        Column(
                            Modifier.clip(MaterialTheme.shapes.large).clickable(enabled = !state.busy) { viewModel.pickKid(kid) }.padding(12.dp),
                            horizontalAlignment = Alignment.CenterHorizontally,
                            verticalArrangement = Arrangement.spacedBy(8.dp),
                        ) {
                            KidAvatar(kid, avatarPhotos, 88)
                            Text(kid.displayName, style = MaterialTheme.typography.titleMedium)
                        }
                    }
                }
                if (picker.kids.isEmpty()) Text(stringResource(R.string.device_no_kids))
                if (picker.parents.isNotEmpty()) {
                    OutlinedButton(onClick = { viewModel.chooseParent(true) }, modifier = Modifier.fillMaxWidth()) {
                        Text(stringResource(R.string.device_parent))
                    }
                }
            }
        }

        if (state.choosingParent && picker != null) {
            AlertDialog(
                onDismissRequest = { viewModel.chooseParent(false) },
                title = { Text(stringResource(R.string.device_which_parent)) },
                text = {
                    Column {
                        picker.parents.forEach { p ->
                            TextButton(onClick = { viewModel.pickParent(p) }, modifier = Modifier.fillMaxWidth()) { Text(p.displayName) }
                        }
                    }
                },
                confirmButton = {},
                dismissButton = { TextButton(onClick = { viewModel.chooseParent(false) }) { Text(stringResource(R.string.action_cancel)) } },
            )
        }
        state.pinTarget?.let { target ->
            PinPadDialog(
                title = when (target) {
                    is PinTarget.Kid -> stringResource(R.string.device_pin_kid, target.kid.displayName)
                    is PinTarget.Parent -> stringResource(R.string.device_pin_parent, target.parent.displayName)
                },
                error = state.pinError?.let { pinErrorText(it) },
                busy = state.busy,
                onSubmit = viewModel::submitPin,
                onCancel = viewModel::cancelPin,
            )
        }
    }
}

/**
 * The kid's chores. The [TodayViewModel] lives in [store], which [DeviceViewModel] keeps for the
 * session: it survives rotation, and the session ending clears it so a finished session's token
 * doesn't linger. A new session starts with fresh data.
 */
@Composable
private fun KidHome(
    access: KidAccess,
    store: ViewModelStoreOwner,
    todayViewModel: (KidAccess) -> TodayViewModel,
    inboxViewModel: (KidAccess) -> InboxViewModel,
    onLock: (() -> Unit)?,
    onParent: (() -> Unit)?,
    onUnauthorized: () -> Unit,
) {
    val vm = viewModel<TodayViewModel>(viewModelStoreOwner = store) { todayViewModel(access) }
    val inbox = viewModel<InboxViewModel>(viewModelStoreOwner = store) { inboxViewModel(access) }
    val inboxState by inbox.state.collectAsStateWithLifecycle()
    var inboxOpen by rememberSaveable { mutableStateOf(false) }
    LifecycleResumeEffect(inbox) {
        inbox.onResume()
        onPauseOrDispose {}
    }
    if (inboxOpen) {
        InboxScreen(
            viewModel = inbox,
            onBack = { inboxOpen = false },
            onOpen = { entry ->
                // Only a chore on today's list opens; for anything else the inbox stays and the item is marked read.
                val id = entry.instanceId
                if (id != null && vm.open(id)) inboxOpen = false
            },
            onSignedOut = onUnauthorized,
        )
    } else {
        KidHomeScreen(vm, access.name, onLock, onParent, onUnauthorized, inboxUnread = inboxState.unread, onInbox = { inboxOpen = true })
    }
}

@Composable
private fun Centered(content: @Composable () -> Unit) {
    Column(
        Modifier.fillMaxSize().padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) { content() }
}

@Composable
internal fun Countdown(until: Instant) {
    var left by remember { mutableLongStateOf(until.epochSecond - Instant.now().epochSecond) }
    LaunchedEffect(until) {
        while (left > 0) {
            delay(1000)
            left = until.epochSecond - Instant.now().epochSecond
        }
    }
    val s = left.coerceAtLeast(0)
    Text(stringResource(R.string.device_parent_left, s / 60, s % 60))
}

@Composable
private fun pinErrorText(e: PinError): String = when (e) {
    PinError.Wrong -> stringResource(R.string.device_pin_wrong)
    is PinError.Locked -> pluralStringResource(R.plurals.device_pin_locked, e.minutes.toInt(), e.minutes.toInt())
    is PinError.Failed -> stringResource(R.string.login_error_failed, e.reason)
}
