# Chore schedules & assignment

Status: accepted (planning session, 2026-10-30). Builds on [points.md](points.md) for the instance lifecycle and day close.

## Decisions

| Topic | Decision |
|---|---|
| Repeat patterns | Specific weekdays; every N days/weeks; monthly variants (specific days incl. last day, Nth weekday); **flexible "N times per week"**; one-off. |
| Time slots | A chore can have **several due times per day**; each slot produces its own instance (e.g. feed dog 07:00 and 18:00). |
| Assignment modes | **Fixed kid(s)**, **everyone eligible**, **rotation**, **fair auto-pick**, and **shared/team**. |
| Generation | **Rolling horizon**: instances generated ~7 days ahead. |
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

Common fields on every rule: `startDate` (anchor for intervals), optional `endDate`, and `slots`: a list of due times
(`["07:00", "18:00"]`; empty = due at day close). All in the household timezone.

- Day 31 in a 30-day month: skipped (use `-1` for "last day"). Documented in the editor.
- **Week boundaries**: household setting `week_start` (default Monday). Used by weekly intervals, flexible chores,
  week close and the "this week" views.
- **Flexible chores** generate one instance per required completion at week start, all due at week close. Each
  completion closes one instance; leftovers become `missed` at week close.

## Assignment modes

| Mode | Instances per occurrence | Assignee |
|---|---|---|
| `fixed` | one per listed kid | the listed kids |
| `everyone` | one per eligible kid | all eligible, present kids |
| `rotation` | one | next kid in the ordered list |
| `auto` | one | eligible kid with the lowest load |
| `shared` | one, assigned to a **group** | any group member can complete it |

**Eligibility** (applied in all modes): not away, household not paused, meets minimum age (computed from birth
date on the instance's date) unless a per-kid override exists.

**Rotation**: the chore stores an ordered kid list and a cursor. For each occurrence, walk from the cursor to the
first eligible kid. If a kid is skipped, they're added to a `owed` list and get the next occurrence they're
eligible for before the cursor resumes. So the skipped kid "keeps their place" and turns even out.

**Auto (fair) pick**: load = sum of `estimated_minutes` of the kid's instances over the 7 days ending on the
instance date (missing minutes counts as a household default, e.g. 10). Pick the lowest load; tie-break on fewest
recent `auto` picks of this chore, then a stable order. It's deterministic, with no randomness (unlike ownit), so
regenerating the horizon is reproducible and testable.

**Shared**: completer taps done and optionally tags helpers from the group. Points policy per chore:
`completer_takes_all` or `split_evenly` (remainder to the completer). The penalty when missed goes to every group
member, or to none, as a per-chore setting.

## Rolling horizon generation

- A scheduler in the Go binary runs at day close (per household timezone) and on startup, keeping instances
  materialized for `[today, today + 7 days]`.
- Each instance stores `(chore_id, occurrence_date, slot)` with a unique constraint, so generation is idempotent.
- Instances are **untouched** when `status = open`, no evidence, no swap, and not manually reassigned.
- **Chore edited**: delete untouched instances from tomorrow on (today too if the due slot hasn't passed and it's untouched) and regenerate.
- **Away / pause added**: untouched instances in the range are removed (no penalty) and the range is regenerated
  (rotation/auto pick someone else; `fixed`/`everyone` simply skip).
- Rotation cursors and auto loads are computed in generation order, so future instances are a forecast and may shift if things change. The UI should say "planned" for days after today.

## Disruptions

- **Kid away**: `absences(kid_id, start_date, end_date, note)`. Parent-created. Shows on the calendar.
- **Household pause**: same as an absence for everyone. Streaks don't break on paused days.
- **Swap/trade**: a kid proposes trading instance A (theirs) for instance B (sibling's), or just handing one over.
  The sibling accepts; household setting `swaps_require_parent_approval`. On completion, both instances change
  assignee and are marked as manually reassigned (not regenerated).
- Parents can always **reassign** any open instance directly.

## Routines

`routines(name, icon, window_start, window_end, kid scope, ordered chore list)`. Display only: the kid app shows
active routines as checklists during their window, and remaining chores below. Completion and points are per chore.
A "routine complete" moment can feed per-routine streaks later (not v1).

## Open questions

- Should the horizon length be a setting (7 days default)?
- Shared chores and the AI check: whose photos count, and can helpers add evidence?
- Do swaps need a time limit (expire if not accepted before the earlier instance is due)?
