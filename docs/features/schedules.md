# Chore schedules & assignment

Status: accepted (planning session, 2026-10-30). Builds on [points.md](points.md) for the instance lifecycle and day close.

## Decisions

| Topic | Decision |
|---|---|
| Repeat patterns | Specific weekdays; every N days/weeks; monthly variants (specific days incl. last day, Nth weekday); **flexible "N times per week"**; one-off. |
| Time slots | A chore can have **several due times per day**; each slot produces its own instance (e.g. feed dog 07:00 and 18:00). |
| Assignment modes | **Fixed kid(s)**, **everyone eligible**, **rotation**, **fair auto-pick**, and **shared/team**. |
| Generation | **Rolling horizon**: instances generated **7 days** ahead (fixed, not a setting). |
| Edits | Future, untouched instances are regenerated. Today's, started and finished instances keep their values. |
| Age gating | Optional **minimum age**, eligibility updates automatically on the kid's birthday; per-kid parent override (force allow/deny). |
| Rotation gaps | Away/ineligible kid is **skipped but keeps their place**; they take the next turn so it evens out. |
| Fairness metric | **Estimated minutes** over a rolling 7 days (falls back to chore count when minutes are unset). |
| Shared chores | One instance for a group; points go to the completer or are **split among helpers** the kid tags. |
| Disruptions | **Kid away** (date range), **household pause**, **swap/trade requests**. |
| Routines | **Display groups**: named, ordered, with a time window; shown as a checklist; each chore still scores separately. |
| Flexible chores | Each completion earns points; at **week close**, each missing completion is `missed` (penalty if set). |

## Schedule rule model

Stored as JSON on the chore and validated server-side. Deliberately a small subset of iCal RRULE semantics, so
it's easy to render in a form, without supporting arbitrary RRULE strings.

```jsonc
// one-off
{ "type": "once", "date": "2026-11-08" }

// daily / every N days
{ "type": "daily", "interval": 1 }

// weekly: specific weekdays, every N weeks
{ "type": "weekly", "interval": 2, "weekdays": ["sat"] }

// monthly by day: 1st and 15th; -1 = last day of month
{ "type": "monthly", "interval": 1, "monthDays": [1, 15] }
{ "type": "monthly", "interval": 1, "monthDays": [-1] }

// monthly by Nth weekday: 2nd Tuesday; -1 = last
{ "type": "monthly", "interval": 1, "nthWeekday": { "n": 2, "weekday": "tue" } }

// flexible: N completions any day in the week
{ "type": "flexible_weekly", "timesPerWeek": 2 }
```

Common fields on every rule except `once`: `startDate` (required; anchors intervals) and optional `endDate`
(inclusive). Due times within a day are the chore's `slots` (`["07:00", "18:00"]`; empty = due at day close), not part
of the rule. All in the household timezone. A chore with no rule is unscheduled.

Implemented in `server/internal/schedule`:

- **Normalize** fills defaults (`interval` 1), drops fields that don't apply to the type, and sorts/dedupes lists, so
  equal rules store identically. **Validate** reports problems at their JSON location (`body.schedule.weekdays`).
- **Intervals** are anchored on `startDate`: every N days counts days from it; every N weeks counts weeks from the week
  containing it (so `week_start` matters); every N months counts calendar months from its month.
- **Due instants**: `slot` on the date via the household calendar (a slot in a DST gap resolves to the gap's end; a
  repeated wall time to the earlier instant), or the household day close without slots.
- **Preview**: `POST /chores/preview-schedule` `{rule, slots, count ≤ 50}` returns the normalized rule and the next
  occurrences from today (POST because the rule is a body). The web editor shows it live as the rule is edited.

- Day 31 in a 30-day month: skipped (use `-1` for "last day"). Documented in the editor. The same goes for a 5th
  weekday in a month that has only four.
- **Week boundaries**: household setting `week_start` (default Monday). Used by weekly intervals, flexible chores,
  week close and the "this week" views.
- **Flexible chores** generate one instance per required completion at week start (or the start date in the first
  week), all due at week close. Slots don't apply. Each
  completion closes one instance; leftovers become `missed` at week close.

## Assignment modes

| Mode | Instances per occurrence | Assignee |
|---|---|---|
| `fixed` | one per listed kid | the listed kids |
| `everyone` | one per eligible kid | all eligible, present kids |
| `rotation` | one | next kid in the ordered list |
| `auto` | one | eligible kid with the lowest load |
| `shared` | one, assigned to a **group** | any group member can complete it |

The chore stores `assignment: {mode, kidIds, sharedPoints, sharedPenalty}`; absent means `everyone`. `kidIds` is the
fixed kids, the rotation order, the auto pool (empty = every kid) or the shared group: there's no separate group
entity, the group is the chore's kid list. Implemented as a pure planner in `server/internal/assign`: given the kids,
the chore, its occurrences in order, the rotation state and a fairness ledger, it returns the same instances every
time, so regenerating the horizon is reproducible.

**Eligibility** (applied in all modes): not archived, not denied by an override, not away / household not paused
(Phase 2; the planner takes an `Away` hook), and meets the minimum age on the instance's date unless an override
allows the kid. A kid without a birth date isn't age-gated. Archived kids may stay in a chore's list; they're skipped.

**Rotation**: the chore stores an ordered kid list and a cursor. For each occurrence, walk from the cursor to the
first eligible kid. If a kid is skipped, they're added to a `owed` list and get the next occurrence they're
eligible for before the cursor resumes. So the skipped kid "keeps their place" and turns even out. Kids who leave for
good (archived, denied, removed from the list) are never owed. When nobody is eligible there's no instance and
nobody is owed. The cursor and owed list are stored on the chore (`rotation_state`) and reset when the mode or the
kid list changes.

**Auto (fair) pick**: load = sum of `estimated_minutes` of the kid's instances over the 7 days ending on the
instance date (missing minutes counts as the household `defaultChoreMinutes`, default 10). Pick the lowest load; tie-break on fewest
recent `auto` picks of this chore (same window), then a stable order (the pool's order, or kid id). It's deterministic, with no randomness (unlike ownit), so
regenerating the horizon is reproducible and testable.

**Shared**: completer taps done and optionally tags helpers from the group. The **completer** must satisfy the
evidence rules; tagged helpers can attach extra evidence until a parent decides. The AI check runs on the combined set. Points policy per chore:
`completer_takes_all` or `split_evenly` (remainder to the completer). The penalty when missed goes to every group
member, or to none, as a per-chore setting. For fairness, each member's load gets the chore's minutes divided by the
group size (rounded up).

## Rolling horizon generation

- A scheduler in the Go binary runs at day close (per household timezone) and on startup, keeping instances
  materialized for `[today, today + 7 days]`.
- Each instance stores `(chore_id, occurrence_date, slot)` (plus a flexible week's `seq` and the kid) with a unique
  constraint, so generation is idempotent.
- Instances are **untouched** when `status = open`, no evidence, no swap, and not manually reassigned.
- **Chore edited**: delete untouched instances from tomorrow on (today too if the due slot hasn't passed and it's untouched) and regenerate.
  A slot added or moved later today isn't created today for a kid who already has a kept instance of the chore today
  (done, or already due): the edit takes full effect tomorrow. For rotation, auto and shared chores, any kept
  instance of the chore today defers new slots to tomorrow, whoever it belongs to.
- An instance without a slot is due at its kid's own day close (`dayEndOverride`), or week close when flexible. A
  shared instance is due at the household day end.
- **Away / pause added**: untouched instances in the range are removed (no penalty) and the range is regenerated
  (rotation/auto pick someone else; `fixed`/`everyone` simply skip).
- Rotation cursors and auto loads are computed in generation order, so future instances are a forecast and may shift if things change. The UI should say "planned" for days after today.

Built (#23, `internal/horizon`): the `chore_instances` table holds one row per instance, identified by
`(chore_id, occurrence_date, slot, seq, kid)` (unique; `seq` numbers a flexible week's completions, `kid_id` is NULL
for a shared instance, whose group is in `chore_instance_members`). `status` is `open`, `submitted`, `approved`,
`missed` or `excused`; `flexible`, `auto_assigned` and `manually_reassigned` are flags. `horizon.Generator.Generate`
regenerates the whole household in one transaction (ids are never reused: `AUTOINCREMENT`), at each household day end (`HouseholdDayCloses`), once at
startup, and on `household.calendar_changed`, `chore.changed` and `kid.changed`. Instances that are touched
(not `open`, or manually reassigned) or already due are frozen; the others in the horizon are re-planned and updated
in place when their identity is unchanged, so a second run changes nothing. Occurrences already due when planned
(a slot earlier today) get no instance. Flexible chores are expanded from the week start, so this week's
completions stay in the horizon until week close. The fairness ledger starts with the frozen instances from a
week before this week's start; chores are planned by id and occurrences in order. Each rotation instance stores the
state after its turn; `chores.rotation_state` is the state after the chore's last instance before the horizon, the
walk takes each kept instance's state in turn order (even one whose slot was since removed), and editing the
rotation's mode or kids clears both, so it restarts from its first kid. A chore with history (an instance that's
touched or already due) can't be deleted (409 `chore_has_history`); parents disable it. `GET /instances?kidId&from&to` (default
today to today+7, at most 62 days, `chores:read`) lists instances with `planned` for days after today; kids get only
their own, including shared ones they're a member of (`CanAccessKid`). Evidence (#27) must extend the "untouched"
check in `horizon.existing.untouched`.

## Disruptions

- **Kid away**: `absences(kid_id, start_date, end_date, note)`. Parent-created. Shows on the calendar.
- **Household pause**: same as an absence for everyone. Streaks don't break on paused days.
- **Swap/trade**: a kid proposes trading instance A (theirs) for instance B (sibling's), or just handing one over.
  The sibling accepts; household setting `swaps_require_parent_approval`. On completion, both instances change
  assignee and are marked as manually reassigned (not regenerated).
  - A swap request **expires after 30 minutes** without a sibling response (both kids notified).
  - After expiry the requester can **resubmit it to a parent**, who can approve it without the sibling's acceptance
    (or deny it). The sibling is notified of the outcome.
  - A request also lapses if either instance is no longer `open`.
- Parents can always **reassign** any open instance directly.

## Routines

`routines(name, icon, window_start, window_end, kid scope, ordered chore list)`. Display only: the kid app shows
active routines as checklists during their window, and remaining chores below. Completion and points are per chore.
A "routine complete" moment can feed per-routine streaks later (not v1).

## Open questions

- None outstanding.
