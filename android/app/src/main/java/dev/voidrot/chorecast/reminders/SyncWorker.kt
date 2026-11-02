package dev.voidrot.chorecast.reminders

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import dev.voidrot.chorecast.ChorecastApp
import dev.voidrot.chorecast.core.Fetch
import dev.voidrot.chorecast.core.planReminders
import dev.voidrot.chorecast.core.reminderSettings
import dev.voidrot.chorecast.data.AppMode
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.io.IOException
import java.time.Instant

/**
 * Fetches what a paired kid's own device needs and replaces the reminder schedule from it. Parent
 * installs and shared tablets have no single kid to remind, so there the schedule is just cleared
 * (a parent has no chores of their own, and the inbox screen is a separate change).
 */
class SyncWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val app = applicationContext as ChorecastApp
        val c = app.container
        val scheduler = c.reminderScheduler
        val url = c.settings.serverUrl
        val token = c.tokenStore.read()
        if (c.settings.mode != AppMode.DEVICE || url == null || token == null) return stop(scheduler)

        val device = try {
            c.deviceClient.picker(url, token)?.device
        } catch (_: IOException) {
            return Result.retry()
        } ?: return stop(scheduler) // the token no longer works: the device screen handles re-pairing
        val kidId = device.kidId?.takeIf { !device.shared } ?: return stop(scheduler)

        // null covers offline, a refused token and a server error alike, so retry a few times and then wait for the next run.
        val household = c.householdInfo.get(url, token) ?: return if (runAttemptCount < MAX_ATTEMPTS) Result.retry() else Result.success()
        val kid = when (val f = c.kidChores.profile(url, token, kidId)) {
            is Fetch.Ok -> f.value
            Fetch.Unauthorized, Fetch.NotFound -> return stop(scheduler)
            is Fetch.Unreachable, is Fetch.Failed -> return Result.retry()
        }
        val instances = when (val f = c.kidChores.upcoming(url, token, kidId)) {
            is Fetch.Ok -> f.value
            Fetch.Unauthorized, Fetch.NotFound -> return stop(scheduler)
            is Fetch.Unreachable, is Fetch.Failed -> return Result.retry()
        }
        val settings = reminderSettings(household, kid) ?: return stop(scheduler)

        // One sync at a time may clear and re-enqueue, or two runs could interleave and drop reminders.
        replaceLock.withLock {
            val now = Instant.now()
            scheduler.replace(planReminders(instances, settings, now), now)
        }
        return Result.success()
    }

    private suspend fun stop(scheduler: ReminderScheduler): Result {
        replaceLock.withLock { scheduler.clear() }
        return Result.success()
    }

    private companion object {
        const val MAX_ATTEMPTS = 3
        val replaceLock = Mutex()
    }
}
