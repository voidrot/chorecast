package dev.voidrot.chorecast.core

import dev.voidrot.chorecast.api.models.KidProfile
import dev.voidrot.chorecast.api.models.ParentProfile
import java.time.Instant
import kotlin.time.Duration
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.minutes
import kotlin.time.Duration.Companion.seconds

/** A session open on a shared device: a kid, or a parent elevated with their PIN. */
sealed interface ActiveSession {
    val token: String

    data class Kid(val kid: KidProfile, override val token: String) : ActiveSession
    data class Parent(val parent: ParentProfile, override val token: String, val expiresAt: Instant) : ActiveSession
}

/** Why a PIN was not accepted, as the PIN pad shows it. */
sealed interface PinError {
    data object Wrong : PinError
    data class Locked(val minutes: Long) : PinError
    data class Failed(val reason: String) : PinError

    companion object {
        /** Lockout length when the server doesn't say. */
        private const val DEFAULT_LOCKOUT_SECONDS = 900L

        /** The error to show for a failed session start; null when it started or the device was unpaired. */
        fun from(result: SessionResult): PinError? = when (result) {
            is SessionResult.Started, SessionResult.Unpaired -> null
            SessionResult.WrongPin -> Wrong
            is SessionResult.Locked -> Locked(((result.retryAfterSeconds ?: DEFAULT_LOCKOUT_SECONDS) + 59) / 60)
            is SessionResult.Unreachable -> Failed(result.reason)
            is SessionResult.Failed -> Failed(result.code)
        }
    }
}

/**
 * When the open session ends by itself. A kid session locks after [idle] without a touch. A parent
 * elevation ends at the server's expiry and a touch never extends it.
 */
class SessionLock(private val now: () -> Instant, private val idle: Duration = KID_IDLE) {
    private var session: ActiveSession? = null
    private var lastTouch: Instant = Instant.EPOCH

    fun open(session: ActiveSession) {
        this.session = session
        lastTouch = now()
    }

    fun close() {
        session = null
    }

    fun touch() {
        if (session is ActiveSession.Kid) lastTouch = now()
    }

    /** Time until the session ends, or null when none is open. Zero once it is due. */
    fun remaining(): Duration? = when (val s = session) {
        is ActiveSession.Kid -> (idle - (now().toEpochMilli() - lastTouch.toEpochMilli()).milliseconds).coerceAtLeast(Duration.ZERO)
        is ActiveSession.Parent -> (s.expiresAt.toEpochMilli() - now().toEpochMilli()).coerceAtLeast(0).milliseconds
        null -> null
    }

    fun due(): Boolean = remaining() == Duration.ZERO

    companion object {
        val KID_IDLE = 2.minutes
    }
}

/** What the device does on a heartbeat tick. */
enum class HeartbeatStep { CheckSession, RefreshPicker, Nothing }

object Heartbeat {
    val INTERVAL = 30.seconds

    /**
     * Until push exists, the device notices remote changes by checking in: every tick while a session
     * is open (a parent's remote lock ends it), every second tick at the picker (new kids, or the
     * device was unpaired).
     */
    fun step(tick: Int, sessionOpen: Boolean, loading: Boolean): HeartbeatStep = when {
        sessionOpen -> HeartbeatStep.CheckSession
        tick % 2 == 0 && !loading -> HeartbeatStep.RefreshPicker
        else -> HeartbeatStep.Nothing
    }

    /** True when the server no longer accepts the session token (locked remotely, expired or revoked). */
    fun sessionEnded(check: SessionCheck): Boolean = check is SessionCheck.SignedOut
}

/** The digits typed on the PIN pad. Kid and parent PINs are both 4 to 8 digits. */
data class PinEntry(val digits: String = "") {
    val canSubmit: Boolean get() = digits.length >= MIN_LENGTH

    fun append(digit: Char): PinEntry =
        if (digit in '0'..'9' && digits.length < MAX_LENGTH) PinEntry(digits + digit) else this

    fun backspace(): PinEntry = PinEntry(digits.dropLast(1))

    companion object {
        const val MIN_LENGTH = 4
        const val MAX_LENGTH = 8
    }
}

/** Where a parent is in the screens of an elevated tablet. */
sealed interface ParentPage {
    data object Overview : ParentPage
    data object Review : ParentPage
    data class Kid(val kidId: Long) : ParentPage
}
