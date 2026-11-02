package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.KidProfile
import dev.voidrot.chorecast.api.models.ParentProfile
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Instant
import kotlin.time.Duration
import kotlin.time.Duration.Companion.minutes
import kotlin.time.Duration.Companion.seconds

private class FakeClock(var instant: Instant = Instant.parse("2026-11-02T10:00:00Z")) : () -> Instant {
    override fun invoke() = instant

    fun advance(d: Duration) {
        instant = instant.plusMillis(d.inWholeMilliseconds)
    }
}

class SessionLockTest {
    private val clock = FakeClock()
    private val lock = SessionLock(clock)
    private val kid = ActiveSession.Kid(KidProfile(id = 1, slug = "ada", displayName = "Ada", color = "#fff", pinRequired = false), "kid-token")
    private fun parent(expiresIn: Duration = 2.minutes) = ActiveSession.Parent(
        ParentProfile(id = 7, displayName = "Sam"),
        "parent-token",
        clock.instant.plusMillis(expiresIn.inWholeMilliseconds),
    )

    @Test
    fun nothingOpenMeansNothingToWaitFor() {
        assertNull(lock.remaining())
        assertFalse(lock.due())
    }

    @Test
    fun kidSessionLocksAfterTwoIdleMinutes() {
        lock.open(kid)
        assertEquals(2.minutes, lock.remaining())
        clock.advance(119.seconds)
        assertFalse(lock.due())
        clock.advance(1.seconds)
        assertTrue(lock.due())
    }

    @Test
    fun touchRestartsTheKidIdleWindow() {
        lock.open(kid)
        clock.advance(90.seconds)
        lock.touch()
        clock.advance(90.seconds)
        assertFalse(lock.due())
        assertEquals(30.seconds, lock.remaining())
        clock.advance(30.seconds)
        assertTrue(lock.due())
    }

    @Test
    fun touchNeverExtendsAParentElevation() {
        lock.open(parent())
        clock.advance(100.seconds)
        lock.touch()
        assertEquals(20.seconds, lock.remaining())
        clock.advance(20.seconds)
        assertTrue(lock.due())
    }

    @Test
    fun parentElevationFollowsTheServersExpiry() {
        lock.open(parent(expiresIn = 45.seconds))
        assertEquals(45.seconds, lock.remaining())
    }

    @Test
    fun closedSessionStopsTheClock() {
        lock.open(kid)
        lock.close()
        clock.advance(10.minutes)
        assertNull(lock.remaining())
        assertFalse(lock.due())
    }
}

class HeartbeatTest {
    @Test
    fun checksTheSessionOnEveryTickWhileOneIsOpen() {
        assertEquals(HeartbeatStep.CheckSession, Heartbeat.step(1, sessionOpen = true, loading = false))
        assertEquals(HeartbeatStep.CheckSession, Heartbeat.step(2, sessionOpen = true, loading = true))
    }

    @Test
    fun refreshesThePickerOnEverySecondTickWhenIdle() {
        assertEquals(HeartbeatStep.Nothing, Heartbeat.step(1, sessionOpen = false, loading = false))
        assertEquals(HeartbeatStep.RefreshPicker, Heartbeat.step(2, sessionOpen = false, loading = false))
        assertEquals(HeartbeatStep.Nothing, Heartbeat.step(2, sessionOpen = false, loading = true))
    }

    @Test
    fun onlyARefusedTokenEndsTheSession() {
        assertTrue(Heartbeat.sessionEnded(SessionCheck.SignedOut))
        assertFalse(Heartbeat.sessionEnded(SessionCheck.Unreachable("offline")))
    }
}

class PinErrorTest {
    @Test
    fun mapsServerAnswersToWhatThePadShows() {
        assertEquals(PinError.Wrong, PinError.from(SessionResult.WrongPin))
        assertEquals(PinError.Failed("offline"), PinError.from(SessionResult.Unreachable("offline")))
        assertEquals(PinError.Failed("pin_required"), PinError.from(SessionResult.Failed("pin_required")))
    }

    @Test
    fun lockoutRoundsUpToWholeMinutes() {
        assertEquals(PinError.Locked(1), PinError.from(SessionResult.Locked(1)))
        assertEquals(PinError.Locked(1), PinError.from(SessionResult.Locked(60)))
        assertEquals(PinError.Locked(2), PinError.from(SessionResult.Locked(61)))
    }

    @Test
    fun unknownLockoutAssumesFifteenMinutes() {
        assertEquals(PinError.Locked(15), PinError.from(SessionResult.Locked(null)))
    }

    @Test
    fun startedAndUnpairedAreNotPinErrors() {
        val started = SessionResult.Started(DeviceSessionToken("t", 1, Instant.EPOCH))
        assertNull(PinError.from(started))
        assertNull(PinError.from(SessionResult.Unpaired))
    }
}

class PinEntryTest {
    @Test
    fun needsFourDigitsAndStopsAtEight() {
        var pin = PinEntry()
        "123".forEach { pin = pin.append(it) }
        assertFalse(pin.canSubmit)
        pin = pin.append('4')
        assertTrue(pin.canSubmit)
        "56789".forEach { pin = pin.append(it) }
        assertEquals("12345678", pin.digits)
    }

    @Test
    fun ignoresNonDigitsAndBackspaceOnEmpty() {
        assertEquals("", PinEntry().append('x').append('٣').digits)
        assertEquals("", PinEntry().backspace().digits)
        assertEquals("12", PinEntry("123").backspace().digits)
    }
}
