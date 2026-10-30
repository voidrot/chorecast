# Behavior tracking

Status: accepted (planning session, 2026-10-30). Points flow through the ledger in [points.md](points.md);
notifications per [notifications.md](notifications.md).

## Decisions

| Topic | Decision |
|---|---|
| Core | **Behavior entries** are the foundation; goals, check-ins and streaks are optional layers on top. |
| Presets | **Household list with per-kid overrides**: a preset can be hidden for a kid or given a different point value. |
| Who logs | **Parents** (quick log, widget); **kids self-report positives** (needs approval); **kids nominate siblings** (positive only, needs approval); **shared tablet** via parent PIN. |
| Parent PIN | **Per-parent PIN** (4–8 digits) elevates a shared-tablet session to that parent for ~2 min; lockout after 5 failures. |
| Negative entries | **Visible to the kid with the reason and a "way back"**; positives are celebrated more prominently. |
| Way back | **Per-preset linked suggestion** (text, plus an optional bonus chore or positive preset that earns it back), editable when logging; **AI-drafted** suggestion when AI is configured, which the parent edits before saving. |
| Consequences | **Points only.** Privileges (screen time etc.) are rewards that cost points. |
| Goals | Per-kid targets over a period, with a bonus when met. |
| Daily check-ins | Per-kid yes/no scorecard **answered by a parent near day close**; unanswered means no points either way. |
| Behavior streaks | Days in a row without a given negative behavior, with milestone bonuses. |

## Presets

```
behavior_presets(id, name, icon, polarity: positive|negative, points, way_back_text,
                 way_back_bonus_chore_id?, way_back_preset_id?, archived, sort)
behavior_preset_overrides(preset_id, kid_id, hidden bool, points?)
```

- `points` is always positive in the preset; polarity decides the sign in the ledger.
- Effective value for a kid = override `points` if set, else preset `points`. Hidden presets don't appear when
  logging for that kid (multi-kid logging skips them for that kid).
- Presets are archived, not deleted, so history keeps names and icons.
- First-run seeds a small editable starter list (e.g. "Helped without being asked" +10, "Kind to sibling" +5,
  "Talked back" −5, "Didn't listen first time" −3).

## Entries

```
behavior_events(id, kid_id, preset_id?, name, polarity, points, note, way_back_text, way_back_ref,
                source: parent|self_report|nomination, logged_by, nominated_by?, status, local_date, created_at)
```

- **Ad hoc** entries have no `preset_id`; name, polarity and points are entered directly.
- Name, points and way-back are **copied onto the event** so later preset edits don't rewrite history.
- `status`: `recorded` for parent entries (ledger `behavior` entry written immediately); `pending → approved |
  declined` for self-reports and nominations (ledger entry on approval).
- Removing an entry writes a `reversal` (points.md). The event is marked `reversed` and hidden from the kid.
- **Multi-kid logging**: selecting several kids writes one event per kid (each with its own effective value).

### Parent quick log
Parent app home has a **Log behavior** action: pick kid(s) (avatars), then a preset grid split positive/negative,
then an optional note, then save. An Android **home-screen widget** shows avatars and the top 6 presets for one-tap
logging (opens a confirm sheet). Target is no more than 3 taps for a preset entry.

### Kid self-report and nominations
- Self-report: kid picks a **positive** preset (only those visible to them) or writes a short description;
  optional photo (same fresh-evidence rules as chores). Goes to the parent review queue alongside chore
  submissions; the parent can adjust points when approving.
- Nomination: kid picks a sibling plus a positive preset or text ("Sam helped me with homework"). On approval the
  sibling gets the points and a notification naming who nominated them; the nominator gets nothing (avoids
  trading nominations). Rate-limited per kid per day (household setting, default 3).

## Way back

Shown on the kid's view of a negative entry: "Earn it back: help set the table", with a button that opens the linked
bonus chore (claim) or a self-report of the linked positive preset.

- Default comes from the preset; the parent can edit or clear it when logging.
- **AI draft** (only if an AI provider is configured *and* the household enables "AI behavior suggestions", a
  separate toggle from the photo check): when logging a negative entry, the server sends the behavior name, the
  parent's note and the kid's **age only** (no name) and returns a short, constructive suggestion that the parent
  edits or discards. Nothing is saved or shown to the kid without the parent confirming.

## Goals

```
behavior_goals(id, kid_id, title, preset_id, comparison: at_least|at_most, target, period: day|week|month,
               bonus_points, start_date, end_date?, active)
```
- Examples: at least 5 "Kind to sibling" per week (+20); at most 2 "Talked back" per week (+15).
- Evaluated at period close (day close or week close per `week_start`). Met: a `behavior_goal` ledger bonus.
  Missed: no penalty. Progress is shown live on the kid's home ("3 of 5 this week").
- `at_most` goals only count the period if the kid wasn't away for all of it.

## Daily check-ins

```
checkin_items(id, kid_id?, question, points, weekdays, active)   -- kid_id NULL = all kids
checkin_answers(item_id, kid_id, local_date, answer: yes|no, answered_by, answered_at)
```
- 30 min before day close (configurable), parents get a **scorecard** notification: each kid's items as yes/no
  toggles, answerable from the notification or the app. Any parent can answer; first answer wins and is editable
  until the next day close.
- `yes` earns a `checkin` ledger entry. `no` and unanswered give nothing; check-ins never deduct.
- Kids see their scorecard results the next morning in the overview.

## Behavior streaks

- Defined per negative preset (opt-in): "days without *Talked back*", with household milestone bonuses like the
  chore streaks (e.g. 7 days +10, 30 days +50).
- A day counts if the kid had **no recorded entry** of that preset and wasn't away. Logging the behavior resets
  the streak; a reversed entry restores it.

## Trend chart (v1)

Each kid's behavior page in the parent app and web shows a weekly bar chart of positive vs negative entry counts
(and net behavior points) for the last 12 weeks, computed from `behavior_events` (reversed entries excluded).

## Ledger kinds added

`behavior` (exists), `behavior_goal`, `checkin`, `behavior_streak`. Each references its source row.

## Notifications

- Kid (`points` category): behavior entries with note and way-back, goal met, streak milestone, nomination
  received, self-report decision.
- Parent (`reviews` category): self-reports and nominations to approve; scorecard reminder (`reminders` category).

## Open questions

- None outstanding. Nominations are shown to the sibling with the nominator's name.
