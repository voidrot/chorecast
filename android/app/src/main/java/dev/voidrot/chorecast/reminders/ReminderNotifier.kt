package dev.voidrot.chorecast.reminders

import android.Manifest
import android.annotation.SuppressLint
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import dev.voidrot.chorecast.MainActivity
import dev.voidrot.chorecast.R
import dev.voidrot.chorecast.core.ReminderKind
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

/** Shows the reminder notifications. The notification id comes from the reminder key, so a repeat replaces. */
class ReminderNotifier(private val context: Context) {
    fun show(key: String, kind: ReminderKind, dueAt: Instant?, count: Int, firstChoreName: String) {
        if (!canNotify(context)) return
        ensureChannel()
        val time = dueAt?.let { TIME.format(it) }.orEmpty()
        val res = context.resources
        val (title, text) = when (kind) {
            ReminderKind.SLOT_DUE ->
                context.getString(R.string.reminder_slot_title) to context.getString(R.string.reminder_slot_text, firstChoreName, time)
            ReminderKind.DAY_CLOSE ->
                context.getString(R.string.reminder_close_title) to
                    res.getQuantityString(R.plurals.reminder_close_text, count, count, firstChoreName, time)
            ReminderKind.MORNING ->
                context.getString(R.string.reminder_morning_title) to
                    res.getQuantityString(R.plurals.reminder_morning_text, count, count, firstChoreName)
        }
        val open = PendingIntent.getActivity(
            context, 0, Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val notification = NotificationCompat.Builder(context, CHANNEL)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setContentIntent(open)
            .setAutoCancel(true)
            .build()
        notify(key.hashCode(), notification)
    }

    // The permission is checked in canNotify, which lint can't see through.
    @SuppressLint("MissingPermission")
    private fun notify(id: Int, notification: android.app.Notification) =
        NotificationManagerCompat.from(context).notify(id, notification)

    private fun ensureChannel() {
        val channel = NotificationChannel(
            CHANNEL, context.getString(R.string.reminders_channel_name), NotificationManager.IMPORTANCE_DEFAULT,
        ).apply { description = context.getString(R.string.reminders_channel_description) }
        context.getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
    }

    companion object {
        private const val CHANNEL = "reminders"
        private val TIME: DateTimeFormatter =
            DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withZone(ZoneId.systemDefault())

        /** Android 13+ needs the runtime permission; before that, notifications can only be switched off in settings. */
        fun canNotify(context: Context): Boolean {
            val granted = Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
                ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
            return granted && NotificationManagerCompat.from(context).areNotificationsEnabled()
        }
    }
}
