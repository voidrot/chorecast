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

## Open questions

- Webhook event catalog and payload schema versioning: define alongside the API implementation.
