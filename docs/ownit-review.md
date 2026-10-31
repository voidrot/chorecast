# Review of voidrot/ownit (predecessor) — feature inventory

Reviewed 2026-10-30 at ownit `beca016`. ownit is a Django + Celery + Postgres backend (server-rendered admin with
HTMX, plus a django-ninja JSON API) and an Expo/React Native app with parent and child tab sets.
This doc lists what it does, what it planned but never built, and what to change in Chorecast.

## What ownit actually implements

### Chore catalog (managed in the web admin)
- **Chore**: name, description, points, optional due time, estimated minutes, `disabled`.
- **Recurrence**: one-off, daily, weekly (single day-of-week, stored as a string label), monthly (single day-of-month string).
- **Penalty**: `penalize_incomplete` + `penalty_amount` as a *percent* of the chore's points.
- **Age gating**: `age_restricted` + `minimum_age`, compared against the child's `birth_date`.
- **Assignment mode**: `assign_to_all` (every child gets a copy) or one child picked automatically.
- **Supporting catalogs**: Locations, Equipment (with photo and storage location), Tasks (reusable steps with
  equipment and structured steps), instruction video (upload or external URL).

### Assignments (daily instances)
- Celery beat in **UTC**: 00:00 close overdue open assignments → 00:30 create today's assignments → 01:00 recompute points.
- **Auto-assignment fairness**: weighted random, weight `1/(open assignments + completions in last 7 days + 1)`.
  Deduplicates per chore/child/day.
- **Lifecycle** with boolean flags `is_completed`, `pending_approval`, `approved`, `closed`:
  child marks done → *pending approval* → parent **approves** (points awarded), or parent/child **marks incomplete**
  (reopens). Unfinished assignments are **closed** at midnight, and closed + penalized counts as points lost.
- **Evidence**: children attach photos and videos (single or batch upload), and can delete them.
- **History**: parents see completed, approved and closed assignments grouped by date.

### Points
- `DailyChildPoints` per child per day: possible / earned / lost / total, **recomputed from all assignments**.
- API: per-child points summary, running total, and daily series. The parent home shows every child's totals and trends.

### Users & auth
- Django users; roles via groups `parent` / `child`; `birth_date` for age gating.
- django-allauth headless with session tokens; **kids log in with username + password**.
- The app has a "backend config" screen to point it at a self-hosted server URL.
- Parents create users and change passwords in the web admin.

### App screens
- Parent: Home (points overview), Chores (per child), History, Behavior, Profile, chore-details modal.
- Child: Home (today + points), Chores, Behavior, Profile, chore-details modal (complete, add evidence).

## Planned in ownit but never built
- **Behavior tracking**: web page and both app tabs are "Coming soon" placeholders, with copy that mentions
  "streak momentum" and "coaching moments".
- **Rewards / spending points**: nothing; points only accumulate.
- **Notifications**: none.

## Lessons: what to do differently in Chorecast

| ownit issue | Chorecast approach |
|---|---|
| Everything runs on UTC midnight, so a US family's "day" closes in the afternoon | Household **timezone** setting; all scheduling and day-bucketing in local time |
| One-off chores match `is_recurring=False` every day, so they get re-assigned daily | One-off = a single dated instance; recurrence rules only for repeating chores |
| Recurrence limited to one weekday or one month-day string; no "every other week", "Mon/Wed/Fri", or "last day of month" | Proper rule model (RRULE subset: freq, interval, by-day list, by-month-day incl. -1) |
| Four booleans allow invalid combinations | Single `status` enum state machine: `open → submitted → approved` / `rejected→open` / `missed` / `excused` |
| Points recomputed from scratch daily; no way to spend or adjust | Append-only **points ledger** (earn, penalty, behavior, reward redemption, manual adjust); balances are sums |
| Assignment is random-weighted only | Choice per chore: fixed assignee(s), everyone, **rotation** (round-robin), or fair auto-pick |
| Kids need username/password | Device pairing + profile picker + optional PIN (already decided) |
| Celery + Redis + Postgres = 4 containers | In-process scheduler in the Go binary + SQLite |
| Evidence stored via Django storage | Files under `CHORECAST_DATA_DIR/media`, size limits, thumbnailing, retention policy |
| "Excuse" doesn't exist; parents can only reopen | Parent can **excuse** (no penalty) or **reassign** an instance |

## Feature areas to plan for Chorecast
1. **Household & people**: timezone, parents, kids (avatar, color, birth date), devices.
2. **Chore definitions**: points, schedule rule, due time, assignment mode, approval required?, evidence required?,
   penalty, age gate, steps/checklist, location/equipment, instruction media.
3. **Daily instances & workflow**: generation, complete → approve/reject, missed/excused, late completion policy.
4. **Points economy**: ledger, balances, possibly multiple currencies (points vs. money/allowance).
5. **Behavior**: positive/negative behaviors with point values, quick-log by parents, maybe streaks.
6. **Rewards**: catalog, redemption requests, parent approval, stock/cooldowns.
7. **Notifications**: due reminders, approval requests, approved/rejected, reward requests.
8. **History & reporting**: per-child history, trends, completion rate.
9. **Admin**: backups, export, settings.
