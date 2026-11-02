package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.ChoreInstance
import dev.voidrot.chorecast.api.models.DailyWindow
import dev.voidrot.chorecast.api.models.HouseholdSettings
import dev.voidrot.chorecast.api.models.Kid
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import java.time.LocalDate
import java.time.LocalTime
import java.time.OffsetDateTime
import java.time.ZoneId

class RemindersTest {
    private val zone = ZoneId.of("America/Chicago") // UTC-5 in May
    private val day = LocalDate.of(2026, 5, 4)
    private val settings = ReminderSettings(zone, "21:00", DEFAULT_KID_QUIET_HOURS)

    private fun at(date: String, time: String): Instant =
        LocalDate.parse(date).atTime(LocalTime.parse(time)).atZone(zone).toInstant()

    private fun instance(
        id: Long,
        due: Instant,
        slot: String? = null,
        date: LocalDate = day,
        status: ChoreInstance.Status = ChoreInstance.Status.OPEN,
        planned: Boolean = false,
        flexible: Boolean? = null,
    ) = ChoreInstance(
        choreId = id,
        choreName = "Chore $id",
        date = date,
        dueAt = OffsetDateTime.ofInstant(due, zone),
        id = id,
        planned = planned,
        status = status,
        slot = slot,
        flexible = flexible,
    )

    private fun plan(now: Instant, s: ReminderSettings, vararg i: ChoreInstance) = planReminders(i.toList(), s, now)

    @Test
    fun slotReminderComesThirtyMinutesBefore() {
        val r = plan(at("2026-05-04", "12:00"), settings, instance(1, at("2026-05-04", "15:00"), slot = "15:00"))
        val slot = r.single { it.kind == ReminderKind.SLOT_DUE }
        assertEquals("slot:1", slot.key)
        assertEquals(at("2026-05-04", "14:30"), slot.at)
    }

    @Test
    fun reminderTimeAlreadyPastIsDropped() {
        val r = plan(at("2026-05-04", "14:45"), settings, instance(1, at("2026-05-04", "15:00"), slot = "15:00"))
        assertTrue(r.none { it.kind == ReminderKind.SLOT_DUE })
    }

    @Test
    fun finishedAndFlexibleInstancesGetNoReminder() {
        val due = at("2026-05-04", "15:00")
        val finished = listOf(
            ChoreInstance.Status.SUBMITTED, ChoreInstance.Status.APPROVED,
            ChoreInstance.Status.MISSED, ChoreInstance.Status.EXCUSED,
        ).mapIndexed { n, s -> instance(n + 1L, due, slot = "15:00", status = s) }
        val flexible = instance(9, due, slot = "15:00", flexible = true)
        assertTrue(planReminders(finished + flexible, settings, at("2026-05-04", "06:00")).isEmpty())
    }

    @Test
    fun quietHoursHoldASlotReminderUntilTheyEnd() {
        // Due 07:20 tomorrow: 06:50 is quiet, so it goes out at 07:00.
        val i = instance(1, at("2026-05-05", "07:20"), slot = "07:20", date = day.plusDays(1), planned = true)
        val r = plan(at("2026-05-04", "12:00"), settings, i).single { it.kind == ReminderKind.SLOT_DUE }
        assertEquals(at("2026-05-05", "07:00"), r.at)
    }

    @Test
    fun heldReminderThatWouldNotPrecedeTheSlotIsDropped() {
        val i = instance(1, at("2026-05-05", "07:00"), slot = "07:00", date = day.plusDays(1), planned = true)
        assertTrue(plan(at("2026-05-04", "12:00"), settings, i).none { it.kind == ReminderKind.SLOT_DUE })
    }

    @Test
    fun dayCloseReminderListsWhatIsOpen() {
        val quiet = settings.copy(quiet = QuietWindow(LocalTime.of(22, 0), LocalTime.of(7, 0)))
        val r = plan(
            at("2026-05-04", "12:00"), quiet,
            instance(1, at("2026-05-04", "21:00")),
            instance(2, at("2026-05-04", "21:00")),
            instance(3, at("2026-05-04", "21:00"), status = ChoreInstance.Status.APPROVED),
        ).single { it.kind == ReminderKind.DAY_CLOSE }
        assertEquals("close:2026-05-04", r.key)
        assertEquals(at("2026-05-04", "20:00"), r.at)
        assertEquals(at("2026-05-04", "21:00"), r.dueAt)
        assertEquals(2, r.choreCount)
    }

    @Test
    fun dayCloseReminderInsideQuietHoursGoesOutWhenTheyStart() {
        // The raw 20:00 is inside quiet hours that start at 19:30, so it moves back to 19:30.
        val s = settings.copy(quiet = QuietWindow(LocalTime.of(19, 30), LocalTime.of(7, 0)))
        val r = plan(at("2026-05-04", "12:00"), s, instance(1, at("2026-05-04", "21:00")))
            .single { it.kind == ReminderKind.DAY_CLOSE }
        assertEquals(at("2026-05-04", "19:30"), r.at)
    }

    @Test
    fun defaultsWithMidnightDayEndSendTheDayCloseReminderAtEightPm() {
        val s = settings.copy(dayEnd = "00:00")
        val i = instance(1, at("2026-05-05", "00:00"))
        val r = plan(at("2026-05-04", "12:00"), s, i).single { it.kind == ReminderKind.DAY_CLOSE }
        assertEquals(at("2026-05-04", "20:00"), r.at)
        // Once quiet hours have started, there is nothing left to send.
        assertTrue(plan(at("2026-05-04", "21:00"), s, i).none { it.kind == ReminderKind.DAY_CLOSE })
    }

    @Test
    fun syncDuringQuietHoursKeepsAHeldSlotReminder() {
        val i = instance(1, at("2026-05-05", "07:20"), slot = "07:20", date = day.plusDays(1), planned = true)
        val r = plan(at("2026-05-05", "06:55"), settings, i).single { it.kind == ReminderKind.SLOT_DUE }
        assertEquals(at("2026-05-05", "07:00"), r.at)
    }

    @Test
    fun midnightDayEndClosesAtTheFollowingMidnight() {
        val s = settings.copy(dayEnd = "00:00", quiet = QuietWindow(LocalTime.of(1, 0), LocalTime.of(2, 0)))
        val r = plan(at("2026-05-04", "12:00"), s, instance(1, at("2026-05-05", "00:00")))
            .single { it.kind == ReminderKind.DAY_CLOSE }
        assertEquals(at("2026-05-04", "23:00"), r.at)
    }

    @Test
    fun afterDayEndTheHouseholdDayIsTomorrow() {
        // 22:00 is past the 21:00 day end, so "today" is already 5/5 and its close is 5/5 21:00.
        val s = settings.copy(quiet = QuietWindow(LocalTime.of(23, 0), LocalTime.of(7, 0)))
        val i = instance(1, at("2026-05-05", "21:00"), date = day.plusDays(1))
        val r = plan(at("2026-05-04", "22:00"), s, i).single { it.kind == ReminderKind.DAY_CLOSE }
        assertEquals("close:2026-05-05", r.key)
        assertEquals(at("2026-05-05", "20:00"), r.at)
    }

    @Test
    fun morningOverviewIsAtQuietEndOncePerDay() {
        val today = instance(1, at("2026-05-04", "21:00"))
        val tomorrow = instance(2, at("2026-05-05", "21:00"), date = day.plusDays(1), planned = true)

        val early = plan(at("2026-05-04", "05:00"), settings, today, tomorrow).filter { it.kind == ReminderKind.MORNING }
        assertEquals(listOf("morning:2026-05-04", "morning:2026-05-05"), early.map { it.key })
        assertEquals(at("2026-05-04", "07:00"), early[0].at)

        // After 07:00 today's overview is not sent late; tomorrow's stays.
        val later = plan(at("2026-05-04", "08:00"), settings, today, tomorrow).filter { it.kind == ReminderKind.MORNING }
        assertEquals(listOf("morning:2026-05-05"), later.map { it.key })
    }

    @Test
    fun nothingOpenMeansNoReminders() {
        val done = instance(1, at("2026-05-04", "21:00"), status = ChoreInstance.Status.APPROVED)
        assertTrue(plan(at("2026-05-04", "05:00"), settings, done).isEmpty())
    }

    @Test
    fun planningIsRepeatableWithStableKeys() {
        val items = arrayOf(instance(1, at("2026-05-04", "15:00"), slot = "15:00"), instance(2, at("2026-05-04", "21:00")))
        val now = at("2026-05-04", "06:00")
        val a = plan(now, settings, *items)
        assertEquals(a, plan(now, settings, *items))
        assertEquals(a.size, a.map { it.key }.toSet().size)
    }

    @Test
    fun settingsUseKidDayEndOverrideAndQuietHours() {
        val household = HouseholdSettings(
            allowNegativeBalance = false, dayEnd = "21:00", defaultChoreMinutes = 10, mediaRetentionDays = 30,
            name = "H", offlineGraceMinutes = 0, siblingVisibility = HouseholdSettings.SiblingVisibility.PRIVATE,
            timezone = "America/Chicago", weekStart = HouseholdSettings.WeekStart.SUNDAY,
        )
        val kid = Kid(
            color = "#ffffff", displayName = "Sam", id = 1, pinRequired = false, pinSet = false, slug = "sam",
            dayEndOverride = "19:30", quietHours = DailyWindow(start = "21:00", end = "06:30"),
        )
        val s = reminderSettings(household, kid)!!
        assertEquals("19:30", s.dayEnd)
        assertEquals(QuietWindow(LocalTime.of(21, 0), LocalTime.of(6, 30)), s.quiet)
        assertEquals(DEFAULT_KID_QUIET_HOURS, reminderSettings(household, kid.copy(quietHours = null))!!.quiet)
        assertNull(reminderSettings(household.copy(timezone = "Nowhere/Land"), kid))
    }
}
