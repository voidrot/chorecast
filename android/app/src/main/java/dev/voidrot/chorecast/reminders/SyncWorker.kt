package dev.voidrot.chorecast.reminders

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import dev.voidrot.chorecast.ChorecastApp
import dev.voidrot.chorecast.core.Fetch
import dev.voidrot.chorecast.core.planReminders
import dev.voidrot.chorecast.core.reminderSettings
import dev.voidrot.chorecast.data.AppMode
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

        val household = c.householdInfo.get(url, token) ?: return Result.retry()
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

        val now = Instant.now()
        scheduler.replace(planReminders(instances, settings, now), now)
        return Result.success()
    }

    private fun stop(scheduler: ReminderScheduler): Result {
        scheduler.clear()
        return Result.success()
    }
}
