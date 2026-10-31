# Reporting & history

Status: accepted (planning session, 2026-10-30). Draws on the ledger ([points.md](points.md)), instances
([schedules.md](schedules.md)), behavior ([behavior.md](behavior.md)) and the daily/weekly summaries
([notifications.md](notifications.md)).

## Decisions

| Topic | Decision |
|---|---|
| Parent reports | **Per-kid dashboard**, **chore insights**, **fairness view**, **points flow**. |
| History browsing | **Calendar view**, **filterable activity list**, **per-kid ledger**, **per-chore history**. |
| Kid views | **My week**, **achievements/badges**, **my history** (incl. media on past chores), **savings goal progress**. |
| Range | **All time.** Records are kept forever; only media expires (retention setting). Reports default to the last 4 weeks, with custom ranges. |
| Badges | **Built-in rule-based set + parent-made badges** awarded manually. Badges never grant points. Parents can disable badges per household. |
| Print/display | **Printable weekly chart** and **family dashboard mode** (read-only wall/TV view). |
| Report export | **CSV per report** and per filtered list, using the current filters. |
| AI insights | **Optional weekly insight** in the weekly summary, behind its own toggle, off by default, using anonymized aggregates only. |

## Parent reports

All reports are computed on read with SQL over instances, ledger and behavior tables, scoped to a date range in the
household timezone. At household scale (a few kids, ~10k instances a year) this is fast enough on SQLite.
Add covering indexes; add materialized daily rollups only if profiling shows a need.

**Per-kid dashboard** (default landing for a kid in the parent app/web)
- This week vs last week: completion rate (approved ÷ required due, excused excluded), points earned / lost /
  spent, balance, current daily streak and best, pending reviews and requests.
- Sparklines for the last 8 weeks of completion rate and points earned.
- Behavior trend chart (behavior.md) embedded.

**Chore insights** (per chore, over the range)
- Completion rate, miss rate, rejection rate, average resubmissions.
- Timeliness: median completion time relative to due slot (early/late), and the share of completions done in the last
  hour before day close.
- Breakdown by weekday and by kid. Highlights: "missed most on Thursdays", "rejected 40% of the time".

**Fairness view** (household)
- Per kid per week for the last 4 weeks: assigned estimated minutes, assigned count, completed minutes.
- Separated by assignment mode so you can see whether rotation/auto-pick are balancing as intended; shows absences.

**Points flow** (per kid, per month)
- Sources: chores, bonus chores, behavior (+), goals, check-ins, streaks, adjustments, transfers in.
- Sinks: rewards, penalties, behavior (−), transfers out, reversals.
- Shown as a stacked in/out bar per month with a table; reconciles exactly to the ledger balance change.

## History

- **Calendar**: month and week views, per kid or the whole household. Each day cell shows a completion ring colored
  by status (all done / some missed / excused / away / paused / future planned). Clicking a day lists its instances, behavior
  entries and redemptions.
- **Activity list**: one chronological feed of instances (with status changes), behavior events, ledger
  adjustments, redemptions, transfers and swaps. Filters: kid(s), chore, type, status, date range, has evidence.
  Cursor-paginated.
- **Per-kid ledger**: every entry with running balance, kind filter, links to the source (instance, behavior event,
  redemption) and to reversal pairs.
- **Per-chore history**: all instances of a chore with assignee, status, completion time, evidence thumbnails
  (or "media expired"), AI verdict, rejection feedback.
- Kids see their own activity, ledger and instance history, with media on past chores until it expires. Parent-only
  data (audit info, AI confidence, notes marked private) is hidden.

## Kid views

- **My week**: a celebratory summary: chores done this week, points earned, streaks (daily, per-chore, behavior),
  goals progress, and the savings goal bar.
- **Savings goal**: pinned reward with progress (available points ÷ cost) and a "you'll get there in ~N days"
  estimate from the last 4 weeks' average earnings.
- **Achievements**: a badge shelf; newly earned badges are announced (in-app and the `points` notification category).

## Badges

```
badges(id, key?, name, description, icon, kind: builtin|custom, rule JSON?, tier, archived)
badge_awards(badge_id, kid_id, awarded_at, awarded_by?, note, source_ref?)
```

- **Built-in** badges (seeded, rule-based, evaluated at day close): daily streak 7/30/100; per-chore streak 14;
  chores completed 10/100/500; first reward redeemed; first savings goal reached; behavior goal met 5 times;
  "helper" (5 approved nominations given). Parents can disable individual built-in badges.
- **Custom** badges: parents create a name, icon (from the built-in icon set or an uploaded image) and description,
  and award them manually to a kid with an optional note. They can't be rule-based in v1.
- Badges are display-only and never touch the ledger. Each badge is awarded once per kid; tiers are separate badges.
- Household setting `badges_enabled` (default on) hides the feature entirely.

## Printable weekly chart

Web route `/print/week?kids=...&week=...` rendered with print CSS: one page per kid (or all on one), a grid of
chores × days with empty checkboxes, routines grouped, due times, and point values (toggleable). Uses the planned
instances from the rolling horizon. It's a paper aid only; nothing syncs back.

## Family dashboard mode

- A read-only, full-screen web view for a wall tablet or TV browser at `/display`.
- **Pairing**: the display shows a short code and QR; a parent enters or scans it in the app/web to approve
  (device authorization flow). This creates a `display` device token (new token kind) with **read-only access to
  kid-visible household data**, no actions.
- Shows today's chores per kid with status, routines in their window, streaks, savings goals, and the leaderboard
  (only if the household enabled it). Auto-refreshes via server-sent events, with periodic polling as a fallback.
- Respects a dim/night schedule. Revocable from the Devices page like any device.

## Exports

- Every report table and filtered list has **Download CSV** with the active filters (generated on the fly,
  streamed).
- The complete per-kid or household export is in [household.md](household.md).

## AI weekly insight

- Only if an AI provider is configured **and** the household enables "AI insights" (separate toggle).
- At week close the server builds aggregated stats (per-kid completion by weekday, chore miss/rejection rates,
  fairness numbers, behavior counts), with kids labelled **Kid A/B/C** and no names, photos or notes, and asks for 2–4
  short, practical observations for parents. The response is mapped back to names server-side and appended to the
  **weekly summary**. It's never shown to kids.

## Notifications impact

- Adds a **weekly summary** for parents (at week close): per kid completion, points in/out, streaks, goals, badges
  earned, plus the optional AI insight. Same `summary` category as the daily summary.

## Open questions

- None outstanding.
