# Notifications

Status: accepted (planning session, 2026-10-30). Builds on the other feature specs for the events themselves.

## Decisions

| Topic | Decision |
|---|---|
| Kid events | Due reminders; approved/rejected (with feedback and points); reward/transfer/swap decisions and requests; behavior entries, streak milestones, new bonus chores. |
| Parent events | Submissions to review (with Approve/Reject actions); requests (rewards, transfers, swaps); daily summary; problems. |
| Reminders (defaults) | **Before slot due** (30 min, per-chore override); **before day close** (1 h, lists anything open); **morning overview** (end of quiet hours); **escalate to parents** if still open 30 min before day close, **only for chores flagged `escalate`** (default off; e.g. meds, pets). |
| Quiet hours | **Per person, defaults by role**: kids 20:00–07:00, plus an optional **school-hours** window per kid (weekdays + times) that is skipped on household **no-school dates** (holidays/breaks calendar); parents none. Held notifications are delivered when quiet hours end; parent "problems" can bypass. |
| Batching | **Smart batching** for parents: first submission notifies immediately; more within ~10 min collapse into one "Sam submitted 4 chores". |
| Preferences | **Per category × channel toggles.** Android notification channels mirror the categories. |
| Kid control | **Parents set** kids' prefs and quiet hours; kids may mute only `points` and `bonus`. |
| Channels | Android push, **in-app inbox**, **web push** (admin), **email** (SMTP), **webhooks**. |
| Shared tablet | **Household feed without private details**: names the kid, never parent-only info; tap → profile picker. |
| Push transport | **UnifiedPush** (any distributor), **FCM only with the admin's own Firebase project**, always backed by polling. **The project runs no relay or gateway**, so no child data ever passes through project-operated infrastructure. |

## Categories

| Category | Who | Examples | Kid can mute |
|---|---|---|---|
| `reminders` | kids, parents (escalation) | slot due soon, day closing, morning overview, escalation | no |
| `reviews` | parents | submissions, flagged evidence | — |
| `results` | kids | approved, rejected + feedback | no |
| `requests` | both | reward/transfer/swap requests and decisions | no |
| `points` | kids | behavior entries, streak milestones, adjustments, gifts | yes |
| `bonus` | kids | new bonus chore available | yes |
| `summary` | parents | daily digest at day close; weekly digest at week close (with optional AI insight) | — |
| `problems` | parents | duplicate evidence flags, device clock skew, device not synced >48 h, disk space, backup failure, push endpoint failing | — |

## Pipeline

```
domain event ──▶ notification planner ──▶ notifications (inbox rows, per recipient)
                  (recipients, prefs,           │
                   quiet hours, batching)       ▼
                                          deliveries (per channel) ──▶ push / web push / email / webhook workers
```

- Domain code emits events (`assignment.submitted`, `assignment.rejected`, ...) into an in-process outbox table in
  the same transaction as the state change, so nothing is lost on crash.
- The **planner** resolves recipients, applies prefs and quiet hours (computing `deliver_after`), and merges into
  an open batch where applicable (`batch_key`, e.g. `reviews:<parent>:<kid>`).
- Every notification is an **inbox row** first (`notifications(id, recipient_id, category, type, title, body,
  data, deep_link, created_at, read_at, deliver_after)`); channels are just delivery of that row.
- Reminders are scheduled from the rolling horizon: generating or updating an instance (re)schedules its reminder
  jobs; completing it cancels them.
- Delivery workers retry with backoff; endpoints failing for 7 days are disabled and raise a `problems` notice.

## Push transport & privacy

**Push payloads carry no content.** A push is only a wake-up (`{"t":"sync","n":<notification id>}`). The app then
fetches the notification from the server over its authenticated connection and shows it locally. So a
distributor or Google only ever sees an opaque ping, never names, chores or photos.

Per device, the app registers whichever transports are available:

1. **UnifiedPush** (both flavors): the user has a distributor app (ntfy, NextPush, ...) pointing at the
   household's own or a public server. Payloads are additionally Web Push–encrypted (RFC 8291) as UnifiedPush recommends.
2. **FCM, `play` flavor, only if the admin configured it.** The admin creates **their own Firebase project**,
   registers the Android app ID `dev.voidrot.chorecast` in it, and gives the server the service-account JSON
   plus the client config (project ID, app ID, API key, sender ID). The server hands the client config to paired
   devices; the app initializes Firebase **at runtime** with those options and registers a token. The server sends via
   the FCM HTTP v1 API directly. There's no default project and no relay; if unconfigured, FCM is simply unused.
   *Spike needed:* confirm runtime `FirebaseOptions` initialization works for FCM from the Play-signed app with a
   third-party project.
3. **Polling fallback, always on**: WorkManager periodic sync (~15 min, Android minimum) plus locally scheduled
   reminder notifications from synced instance data, so reminders still fire on time with no push at all.

The `foss` flavor never includes Google libraries; option 2 doesn't exist there.

Built (#99, part 2, Android): the polling fallback for a kid's own paired device. WorkManager runs `SyncWorker` about
every 15 minutes on a network constraint (unique periodic work, KEEP) and once each time the app opens. It reads the
device's kid (`/devices/picker`, then `/kids/{id}` for quiet hours and the day-end override), the household settings
(timezone, day end) and the kid's instances, then replaces the whole local schedule. Parent installs and shared
tablets only clear it. `planReminders` (`:core`, `Reminders.kt`) decides what fires: 30 minutes before each slotted
open chore, 1 hour before the kid's day close (what is still open), and a morning overview at the end of the quiet
hours for the day starting then (a morning already past is not sent late, so at most one per day). Submitted,
approved, missed, excused and flexible instances get nothing, and a time already past is dropped. A slot reminder
inside the quiet hours is held until they end (dropped if that is no longer before the slot). A day-close reminder
inside the quiet hours goes out when they start instead, since holding it would pass the close; with the default
quiet hours and a midnight day end that is 20:00. Either way the held time, not the original, is compared with the
current time, so a sync during the quiet hours keeps it. A kid with no quiet hours set gets the 20:00 to 07:00 default, because the API does not expose it. Each reminder is
one-time work named by a stable key (`slot:<instance>`, `close:<date>`, `morning:<date>`), and every sync cancels
the old ones first, so nothing is duplicated. Android 13+ asks for the notification permission once. Finishing a chore in the app triggers a sync, and a reminder that fires after its slot or day close has
passed is skipped. Known limit: school hours are not applied.

## Other channels

- **Web push** (parents in the web admin): standard VAPID Web Push; the server generates its VAPID keypair on first
  run. Same content-free model: the service worker fetches the notification from the API.
- **Email** (SMTP via env config): parent `summary` and `problems` only by default. Emails *do* contain content, so
  they're off until a parent enables them.
- **Webhooks**: household-level URLs with an event filter, HMAC-signed body (`X-Chorecast-Signature`), retries.
  Payloads contain event data; the admin UI warns that this leaves the server. Good for Home Assistant / Discord.

## Shared tablets

A shared-device token receives notifications for every kid profiled on that device, rendered as
"Sam: 2 chores due by 8:00 pm". Only kid-visible categories (`reminders`, `results`, `points`, `bonus`, kid-side
`requests`) are sent; quiet hours use the union of those kids' windows. Tapping opens the profile picker
pre-selected on that kid, requiring their PIN if they have one.

## Daily summary (parents)

Sent at day close (or the end of the parent's quiet hours if later). Per kid: done / missed / excused / pending
review, points earned and lost, balance, streak; plus household items needing action (pending requests, flags).

## Weekly summary (parents)

Sent at week close. Per kid: completion rate vs previous week, points in and out, streaks, goals met, badges earned;
plus the optional AI insight (see [reporting.md](reporting.md)).

## In-app inbox (built, #98)

Built (#98, `internal/inbox`; the Android inbox screen is #99, below). The planner, preferences, quiet hours, batching
and delivery channels above are not built; the inbox is the part every channel will share. `notifications` holds
one row per recipient with a `kind`, what it refers to (`refKind` `instance` or `points_entry`, and `refId`), a
`data` object to render from and `readAt`. No display text is stored. Kinds, written from the domain events:

| Kind | Recipient | Event |
|---|---|---|
| `chore_submitted` | every parent except the one who acted | `instance.completed` that went to `submitted` |
| `chore_approved` | the instance's kids (`data.auto` when the system approved it) | `instance.approved` |
| `chore_rejected` | the instance's kids, with `data.reasonCode` / `reasonText` | `instance.rejected` |
| `chore_missed` | the instance's kids | `instance.missed` |
| `chore_excused` | the instance's kids | `instance.excused` |
| `streak_milestone` | the kid, with `data.days` and `data.points` | `points.entry_posted` of kind `streak_bonus` |

A shared chore's members each get one. Each event is written at most once per recipient (the unique key includes
the event id), so redelivery is harmless. Auto-approvals don't notify parents; the daily summary will cover them.
Archived kids and removed parents get nothing. Rows are purged after 90 days by `maintenance.daily` (the spec named
no period).

API (scope `inbox:use`, held by parents and kids; a shared tablet with a kid profile picked is that kid, and a
parent elevated on a tablet with their PIN reads their own inbox, not the kid's; a bare tablet, display or API key has
no inbox and gets 403 `insufficient_scope`): `GET /inbox` (cursor-paged, newest delivered first, with `unreadCount`),
`POST /inbox/{id}/read` and `POST /inbox/read-all` (both 204; another recipient's id is 404). The caller's own user
id is the recipient, so there is no `kidId` parameter.

The list is ordered by row id (delivery order), not by `createdAt`, which is when the event happened; a late-delivered
event can therefore sit above one that looks newer. `data.choreName` is the chore's name when the notification was
written (at delivery), not its current name. `data.points` is present, 0 included, on `streak_milestone` items only.

Built (#99, Android inbox screen). `InboxClient` and `Inbox.kt` in `:core` list, merge pages and mark read; the app
renders each kind from strings. Kids reach it from their home (kid-bound device and tablet kid session), parents from the
parent home, each with an unread count. A tap marks the item read and opens the chore (kid) or the review queue
(parent). Periodic sync and local reminders are not built yet.

## Open questions

- Webhook event catalog and payload schema versioning: define alongside the API implementation.
