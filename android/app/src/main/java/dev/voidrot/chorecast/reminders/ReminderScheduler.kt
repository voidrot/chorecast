package dev.voidrot.chorecast.reminders

import android.content.Context
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.Data
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import dev.voidrot.chorecast.core.Reminder
import dev.voidrot.chorecast.core.ReminderKind
import java.time.Duration
import java.time.Instant
import java.util.concurrent.TimeUnit

/**
 * Schedules the background sync and the local reminders with WorkManager. Reminders are one-time
 * work with an initial delay, named by [Reminder.key] and tagged [TAG]: a sync cancels everything
 * with the tag and enqueues the new plan, so the schedule is always exactly the last plan.
 */
class ReminderScheduler(context: Context) {
    private val work = WorkManager.getInstance(context)

    /** Sync about every 15 minutes while the network is up. KEEP leaves a schedule that already exists alone. */
    fun schedulePeriodicSync() {
        val request = PeriodicWorkRequestBuilder<SyncWorker>(15, TimeUnit.MINUTES)
            .setConstraints(online)
            .build()
        work.enqueueUniquePeriodicWork(PERIODIC_SYNC, ExistingPeriodicWorkPolicy.KEEP, request)
    }

    /** Sync now (the app opened). A sync already waiting or running covers it. */
    fun syncNow() {
        val request = OneTimeWorkRequestBuilder<SyncWorker>().setConstraints(online).build()
        work.enqueueUniqueWork(SYNC_NOW, ExistingWorkPolicy.KEEP, request)
    }

    /** Replaces every scheduled reminder with [plan]. */
    fun replace(plan: List<Reminder>, now: Instant) {
        clear()
        for (r in plan) {
            val request = OneTimeWorkRequestBuilder<ReminderWorker>()
                .setInitialDelay(Duration.between(now, r.at))
                .setInputData(r.toData())
                .addTag(TAG)
                .build()
            work.enqueueUniqueWork(r.key, ExistingWorkPolicy.REPLACE, request)
        }
    }

    fun clear() {
        work.cancelAllWorkByTag(TAG)
    }

    private fun Reminder.toData() = Data.Builder()
        .putString(KEY, key)
        .putString(KIND, kind.name)
        .putLong(DUE_AT, dueAt?.toEpochMilli() ?: 0L)
        .putInt(COUNT, choreCount)
        .putString(NAME, firstChoreName)
        .build()

    private companion object {
        const val TAG = "reminder"
        const val PERIODIC_SYNC = "sync-periodic"
        const val SYNC_NOW = "sync-now"
        val online: Constraints = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()
    }
}

internal const val KEY = "key"
internal const val KIND = "kind"
internal const val DUE_AT = "dueAt"
internal const val COUNT = "count"
internal const val NAME = "name"

/** Posts one scheduled reminder when its time comes. */
class ReminderWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val kind = inputData.getString(KIND)?.let { runCatching { ReminderKind.valueOf(it) }.getOrNull() }
            ?: return Result.failure()
        val key = inputData.getString(KEY) ?: return Result.failure()
        val dueAt = inputData.getLong(DUE_AT, 0L).takeIf { it > 0 }?.let(Instant::ofEpochMilli)
        ReminderNotifier(applicationContext).show(
            key = key,
            kind = kind,
            dueAt = dueAt,
            count = inputData.getInt(COUNT, 1),
            firstChoreName = inputData.getString(NAME).orEmpty(),
        )
        return Result.success()
    }
}
