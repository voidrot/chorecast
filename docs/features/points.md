# Points system

Status: accepted (planning session, 2026-10-30). Covers earning, spending, corrections, and the AI evidence check.

## Decisions

| Topic | Decision |
|---|---|
| Currency | **One points currency.** Money is just a reward type (e.g. "500 pts = $5"). |
| Crediting chores | **Per-chore `requires_approval`.** Off: points credit when the kid marks it done. On: credit when a parent approves. Either way a parent can reverse later. |
| Due time | **Soft target.** Anything completed before the kid's day closes earns full points. No reduced-points late window. |
| Day close | **Household setting `day_end_time`**, default `00:00`, in the household timezone, with an optional **per-kid override** that ends at or before it (see [household.md](household.md)). "Day close" for a kid always means their effective day end. At day close, open required chores become `missed`. |
| Missed chores | Optional **fixed penalty in points** per chore. Parent can **excuse** a missed chore, which reverses the penalty. |
| Negative balance | **Household setting `allow_negative_balance`.** When off, a deduction is clamped to the current balance (the entry records the clamp). Reversals are excluded on purpose: a reversal is always the exact negation of the entry it undoes, so it can take a balance below zero (owner decision). |
| Corrections | **Append-only ledger.** Mistakes are fixed with a reversing entry linked to the original; nothing is edited or deleted. |
| Manual adjustments | Parent can add or subtract points with a **required note**. **Recurring adjustments** (e.g. weekly base allowance): per kid, amount + note + schedule rule (same model as chores), pausable; writes `adjustment` entries. |
| Transfers | Kids can gift points to a sibling; **parent approves**. |
| Behavior | Parents log positive and negative behaviors, from presets or ad hoc, with point values. Goals, check-ins and behavior streaks add bonuses. See [behavior.md](behavior.md). |
| Streaks | **Daily streak**: every required (non-bonus) chore done that day, excused ones don't break it, bonus points at household milestones (e.g. 3/7/30 days). **Per-chore streaks** for recurring chores. |
| Bonus chores | Optional chores any eligible kid can **claim** (first claim wins) for extra points. No penalty if unclaimed. |
| Rewards | Kid **requests**, points are **held**, parent approves (deducted) or denies (hold released). Rewards can opt in to **needs delivery**: `approved → delivered`, with a parent "to deliver" list. Post-v1: rewards can grant **timed entitlements** (e.g. 30 min PC gaming) and can opt in to **auto-approval when redeemed by an integration**; see [integrations.md](integrations.md). |
| Reward controls | Per-kid availability (kids or min age), limits/cooldowns (N per day/week/month), stock quantity, **savings goals** (kid pins a reward and sees progress). |
| Sibling visibility | **Household setting**: private (own data only) or family leaderboard. The leaderboard ranks by **points earned this week** (gross earned since week start; spending and penalties don't lower rank). |
| AI evidence check | **Advisory + coaching**, never approves on its own. A `not_yet` verdict blocks *auto*-approval (see [approvals-evidence.md](approvals-evidence.md)). Pluggable provider, **off by default**. Details below. |

## Ledger model

Balance is always `SUM(amount)` over a kid's entries. Nothing else stores a balance (a cached total is allowed,
but it is rebuilt from the ledger).

```
points_entries
  id
  kid_id            -> users
  amount            signed integer
  kind              chore | chore_penalty | behavior | streak_bonus | bonus_chore
                    | reward | transfer_in | transfer_out | adjustment | reversal
  local_date        household-local date the entry belongs to (reporting, streaks)
  assignment_id     nullable source refs: exactly one set (or none for adjustment)
  behavior_event_id
  redemption_id
  transfer_id
  reverses_id       -> points_entries; set only for kind = reversal
  note              required for adjustment and reversal
  requested_amount  set when clamped by allow_negative_balance = false
  created_by        user (parent, kid, or NULL = system)
  created_at
```

Rules:
- An entry can be reversed at most once (`UNIQUE(reverses_id)`); a reversal's amount is the negated original and
  its `local_date` is the original's.
- **Holds** are not ledger entries. A pending reward redemption or transfer request reserves points:
  `available = balance − SUM(cost of pending redemptions and transfers)`. A request is allowed only if
  `available ≥ cost`, regardless of `allow_negative_balance`.
- Approving a redemption writes one `reward` entry (−cost). Approving a transfer writes `transfer_out` and
  `transfer_in` with the same `transfer_id` in one transaction.
- Un-approving a chore, un-excusing, or removing a behavior all write `reversal` entries.

Built (#26, `internal/ledger`, server only; the web screen is #34). `points_entries` has the v1 kinds `chore`,
`chore_penalty`, `streak_bonus`, `adjustment` and `reversal`. Instead of one nullable column per source, an entry
names its source as `source_kind` + `source_id` (e.g. `chore_instance`, id) with no foreign key, so later
features add sources without schema changes. `created_by` is NULL for the system. Choices made in implementation:

- **Calling it.** Features post inside their own `bus.InTx` transaction: `ledger.Post(ctx, tx, clock, Posting{…})`
  and `ledger.Reverse(ctx, tx, clock, entryID, note, by)`. The entry, the caller's state change and the
  `points.entry_posted` event commit together. `ledger.Balance` / `ledger.Available` take any `*sqlc.Queries`.
- **At most once.** A unique index allows one `chore`, `chore_penalty` or `streak_bonus` entry per (kind, kid,
  source), and these kinds require a source. A repeat `Post` writes nothing and returns the existing entry with
  `ErrAlreadyPosted`, so at-least-once callers (day close #24, approvals #25) treat that error as done and must
  not return it from their `InTx` function, or their own change rolls back. Undoing a reversal (re-approving,
  un-excusing) means reversing the reversal, not posting again. Streak bonuses (#30) use one source per day and
  milestone (`streak_milestone`, see Streaks). A repeat's returned entry has `ReversedByID` set when it was reversed.
- **Undo and re-apply.** A source's entry and its reversals form a chain C → R1 → R2 ….
  `ledger.Tip(ctx, tx, kidID, kind, src) (entry, inEffect, found, err)` returns the chain's unreversed end, and
  `inEffect` is true when the source's effect stands (an even number of reversals). To undo an effect in force,
  or re-apply one that was undone, `Reverse` the tip, and only when `inEffect` is the state you want to change
  (true to undo, false to re-apply). `Tip` takes only the once-per-source kinds.
- **Clamping.** With `allowNegativeBalance` off, a deduction is clamped so the balance doesn't drop below 0. If
  the balance is already at or below 0, it clamps to 0. A clamped entry is still written, with
  `requested_amount` set. Reversals are never clamped (always the exact negation), so reversing a credit that
  was already spent can take a balance below zero. That exclusion is an owner decision, not an oversight.
- **Reversals** take a required note, copy the original's source and can themselves be reversed. A second
  reversal of one entry fails with `ErrAlreadyReversed` (409 `already_reversed`). Unlike `ErrAlreadyPosted`,
  it is not success. A retry in its own transaction can't see its own earlier reversal, so the error means
  someone else reversed the entry. For example, a parent reverses C and then R1, and an un-approve job's
  `Reverse(C)` gets `ErrAlreadyReversed` while the credit stands. Callers re-read `Tip` instead.
- **`local_date`** is the caller's date (day close passes the closed day). Otherwise it is the kid's current day:
  the day whose window, up to the kid's effective day end, contains now. A reversal gets the `local_date` of the
  entry it reverses (owner decision), so un-approving last week's chore corrects last week.
- **Available** is the balance for now. The API reads `balance` and `available` in one transaction, so they agree. Holds join `ledger.Available` with rewards (Phase 2).
- **API.** `GET /kids/{kid}/points` (`balance`, `available`) and `GET /kids/{kid}/points/entries` (paged, newest
  first, each entry with `balanceAfter` and `reversedById`) need `points:read`. Kids can read only their own.
  `POST /kids/{kid}/points/adjustments` (`amount` ≠ 0, `note`) and `POST /kids/{kid}/points/entries/{entry}/reversal`
  (`note`) need `points:manage` (parents).

## Chore instance lifecycle (points-relevant)

```
            kid completes                     parent approves
open ──────────────────────▶ submitted ─────────────────────────▶ approved  (+points)
  │  (requires_approval = false: straight to approved, +points)       │
  │                              │ parent rejects (with feedback)      │ parent reverses → reversal entry
  │                              └──────────▶ open                     ▼
  │ day closes                                                      open/rejected
  ▼
missed (−penalty if set) ── parent excuses ──▶ excused (penalty reversed)
```

- A `submitted` instance at day close stays `submitted`: the kid did the work, so it's still approvable.
- Bonus chores: `available → claimed → (same flow)`. An unclaimed bonus expires at day close with no penalty.

Built (#24, `internal/dayclose`, server only). Three job kinds close days and weeks: `dayclose.kid_day` at each
kid's effective day end (`KidDayCloses`), `dayclose.kid_week` at the day close of the week's last day
(`KidWeekCloses`), and `dayclose.shared` at the household day end (`HouseholdDayCloses`). Choices made in
implementation:

- **What closes.** Every instance is required in v1 (there's no optional flag yet). A kid's day close takes their
  open non-flexible instances, the week close their open flexible ones. `submitted`, `approved` and `excused` are
  left alone.
- **Shared instances close at the household day end**, for days and flexible weeks alike. Every kid's day end is
  at or before the household's, so by then every member's day has closed, and it's when the horizon makes a
  shared instance due. A member's earlier day close doesn't touch it. The penalty goes to every member in
  `chore_instance_members` when `shared_penalty = 'all'`, to nobody when `'none'`.
- **Due by the close.** A close at instant T takes every open instance with `due_at` ≤ T. That catches up on its
  own: after downtime, or a failed job, the next close also takes every earlier day that didn't close. A slot later
  than a kid's early day end (slot 20:00, day end 19:00) isn't due at that day's close, so the next day's close
  takes it, still dated its own day. A close whose instant is still ahead does nothing (the kid's day end moved
  later after the job was queued). The moved close is a new occurrence, because the schedule's job key includes
  the close instant, so it still runs that day.
- **Per instance, one transaction** (`dayclose.MissInTx`): `UPDATE … SET status = 'missed' WHERE id = ? AND
  status = 'open' RETURNING …` (no row: someone else moved it, so skip). The assignee, the members and the
  chore's penalty are all read inside the transaction, so a reassignment between the list and the close
  penalizes the new assignee. Then a `chore_penalty` entry of −`penalty` per affected kid (source
  `chore_instance`, created by the system), then an `instance.missed` event `{instanceId, kidIds, from, to}`
  (`kidIds` is the assignee or the shared group). A penalty of 0 posts nothing; the instance is still missed.
  `ErrAlreadyPosted` counts as done, and clamping is the ledger's (above). A re-run or duplicate job finds
  nothing open and changes nothing.
- **`local_date`** of a penalty is the instance's day; for a flexible instance it's the week's last day, the day
  the week closed.
- **Horizon ordering.** The horizon regenerates only untouched instances not yet due, and a close only takes
  instances already due, so the two never touch the same instance, whichever runs first at the household day end.
- **Archived kids'** days still close, so their remaining instances are settled instead of piling up until they're
  unarchived.
- **For transitions that reopen an instance (#25).** `dayclose.ClosesAt(ctx, q, instance)` is when a close takes
  the instance, and `dayclose.MissInTx(ctx, tx, clock, id)` misses it inside the caller's transaction. If a
  reopen happens after the close has passed, call `MissInTx` instead of leaving the instance open until the next
  close. `ErrAlreadyPosted` counts as "the penalty stands", which holds only while no transition reopens an
  instance whose penalty was reversed.
- **Schedules** are named after their kind (`day_close:<kind>:kid:<id>`), so another feature scheduling the same
  closes (streaks, #30) keeps its own cursor. Closes find due instances through the partial index
  `chore_instances_open_due`.

Built (#25, `internal/instances`, server only). Every transition except open → missed (day close, #24) is a
function that runs in the caller's `bus.InTx`, so bulk approve (#28) and auto-approve (#29) call them directly:
`Complete`, `Approve`, `Reject`, `Unapprove` (approved → open), `Excuse` and `Reassign`. Each checks its source
status with an atomic `UPDATE … WHERE status = from` (409 `instance_not_open`, `instance_not_submitted`,
`instance_not_approved` or `instance_not_missed` otherwise), writes ledger entries only through `internal/ledger`,
and emits one event, `instance.<completed|approved|rejected|unapproved|excused|reassigned>` with
`{instanceId, kidIds, from, to, by}`. Choices made in implementation:

- **Credit** is posted on approval, or at completion when `requires_approval` is off (one `instance.completed`
  event with `to: approved`, no separate approval event). It goes to the assignee; for a shared instance to the
  completer (`completer_takes_all`) or split with `assign.SplitPoints` across every member, remainder to the
  completer (`split_evenly`). Helper tagging (schedules.md) isn't built, so the split is over the whole group. A
  share of 0 posts nothing. Credit entries are dated to the instance's own day; for a flexible instance that's
  its week's first active day, so a credit is never dated ahead of today (day close dates a flexible penalty to
  the week's last day, when the week closed).
- **Undo and re-apply** follow the ledger's rule: `Unapprove` reverses every credit tip in effect for the instance
  (`ledger.SourceKids`, so also one left for a kid it was reassigned away from), and re-approving reverses a
  reversal instead of posting again, which restores the original amount even if the chore's points changed.
  A single-kid instance is credited only to its current assignee: their earlier credit is restored, or else the
  current points are posted, and a kid it was reassigned away from stays reversed (nobody is paid for another
  kid's work). A shared `completer_takes_all` instance works the same way for its current completer: their
  earlier credit is restored, or else the points are posted, and an earlier completer stays reversed. A shared
  `split_evenly` instance can't be reassigned, so its group is stable: credits follow the split only on the
  first approval, and a re-approval doesn't re-split, even if the points or the completer changed. It restores
  every earlier credit to its kid with its amount and posts nothing new, so the instance's total is always what
  was first credited. `Excuse` reverses every penalty
  tip in effect: the assignee's, or every member's of a shared instance.
- **Reopening a closed day.** Day close takes every open instance due by the close. When a rejection or approval
  reversal reopens an instance whose close has already passed (`dayclose.ClosesAt`: the kid's day or week close
  for their own instances, the household day end for shared ones), the kid can't redo that day, so the same
  transaction hands it to day close (`dayclose.MissInTx`): it becomes `missed` with its penalty and an
  `instance.missed` event, after the `instance.rejected` or `instance.unapproved` one, and the parent gets that
  state back. A parent can still excuse it. While the day is open, it goes back to `open`.
- **Completion** records `completed_by` (the kid who did it) and `completed_at`; both stay after a rejection or
  reversal, which is how the horizon knows the instance was touched and keeps it. Kids complete their own
  instances or shared ones they're a member of. A parent completes for a kid and names the kid for a shared
  instance. An instance dated after today can't be completed (409 `instance_planned`), nor one whose close has
  passed (`dayclose.ClosesAt`) while day close hasn't taken it yet, after downtime or a lagging job (409
  `instance_closed`, nothing written): day close takes it as missed. Accepting an offline completion made before
  the close, within a grace margin, is #35's job. `Complete` checks and attaches the evidence (`checkEvidence`,
  #27; see approvals-evidence.md).
- **Reassign** moves an open single-kid instance to another active kid and sets `manually_reassigned` (clearing
  `auto_assigned`). It records the kid it was planned for in `reassigned_from` (the first, across several
  reassignments), and the horizon counts the instance as that kid's occurrence, so a fixed or everyone chore
  isn't planned for them again. An instance without a slot becomes due at the new kid's own day (or week) close.
  Shared instances can't be reassigned (409 `instance_shared`), nor moved to a kid who already has the same
  occurrence (409 `instance_taken`) or whose close for it has passed (409 `instance_closed`, `dayclose.ClosesAt`
  for the new kid).
- **API** (each returns the updated `ChoreInstance`): `POST /instances/{id}/completion` (`chores:complete`, body
  `{kidId?}`), `…/approval`, `…/rejection` (`{reason?, text?}`), `…/approval/reversal` and `…/excusal` (`{note?}`
  for the reversal entries) with `chores:review`, and `…/reassignment` (`{kidId}`) with `chores:manage`.
  `ChoreInstance` gains the optional `completedBy`, `completedAt`, `rejectionCount` and `lastRejection`.

## Streaks

Evaluated at day close for each kid:
- **Daily streak**: the day counts if the kid had at least one required chore and every required chore is
  `approved`, `submitted` or `excused`. `submitted` counts provisionally; a later rejection doesn't retro-break the streak (decide in implementation if that proves abusable).
- Milestone bonuses are household-configured (`[{days: 3, points: 5}, {days: 7, points: 15}]`) and written as
  `streak_bonus` entries.
- **Per-chore streaks** are computed from instance history, display only for v1 (no bonus).

Built (#30, `internal/streaks`, server only). Choices made in implementation:

- **When.** A kid's day is evaluated in the `dayclose.kid_day` job right after it marks the day's misses
  (`dayclose.Closer.AfterKidDay`), and again for every kid in the `dayclose.shared` job at the household day end
  (`AfterShared`). By the household day end every instance of the day is due, including shared ones and slots
  later than an early day end. A day whose close is still ahead isn't evaluated.
- **Which chores.** Required means every non-flexible instance dated that day: the kid's own and the shared ones
  they're a member of. Flexible instances belong to their week, not a day, so they don't count. An instance still
  `open` when the day is evaluated counts as not done. A day with no required instances is `none`: it neither
  extends nor breaks the streak.
- **Late changes: counted is final, the rest is re-checked.** Each day's outcome is stored (`streak_days`:
  `counted`, `broken` or `none`). A counted day is never re-evaluated, so a later rejection doesn't break it (the
  rule above). A day that didn't count is re-checked at each evaluation for 7 days (`streaks.RecheckDays`), so a
  chore finished after an early day end or a shared chore done before the household day end repairs it the same
  evening, and a miss a parent excuses within the week repairs it at the kid's next close. After downtime, the
  evaluation also fills every day since the last evaluated one.
- **The streak** is recomputed from the stored days at each evaluation: `current` is the run of counted days up
  to the latest evaluated day (`none` days skipped), `best` the longest run. Both are kept in `kid_streaks` with
  the last evaluated day.
- **Milestones** are the household setting `streakMilestones` (household.md). Each time the run reaches a
  milestone's `days` on a counted day, a `streak_bonus` entry of its `points` is posted, dated that day, from the
  system, with the note "N-day streak". The source is (`streak_milestone`, YYYYMMDD × 10000 + days), so each (day,
  milestone) posts once however often the day is evaluated, and a new run that reaches the milestone again earns
  it again. A run is paid each milestone at most once: when a repair joins two runs, a milestone already paid on
  any day of the joined run isn't paid again on the earlier day it now falls on, while a milestone the joined run
  reaches for the first time is paid, even on a day that already had a shorter one. Bonuses already posted are
  never reversed. Milestones are checked only from the evaluated days on, so a changed setting applies from then.
- **Per-chore streaks** (`kid_chore_streaks`) are recomputed from the kid's instance history at each evaluation,
  per recurring chore (any schedule but `once`; flexible instances left out): `current` is the latest run of
  occurrences that were approved, submitted or excused, `best` the longest. They follow the instances' current
  statuses, so a later rejection does break them.
- **API.** `GET /kids/{kid}/streaks` (`points:read`; kids only their own) returns `daily` (`current`, `best`,
  `lastDay`) and `chores` (`choreId`, `current`, `best`), as of the last evaluated day.

## AI evidence check

Optional, **off by default**, enabled by a parent in Settings, which states plainly where photos are sent.

- **Providers** behind one Go interface: an OpenAI-compatible endpoint (covers Ollama, vLLM, LM Studio and OpenAI)
  and Anthropic (Claude). Config: base URL, API key, model.
- **Per-chore toggle** `ai_check_enabled`. The rubric is built from the chore description, its **steps/checklist**,
  a parent-written **AI instructions** field, and parent-uploaded **reference photos** ("this is what done looks like").
- **Kid flow (coaching)**: the kid takes a photo, the server returns a verdict (`looks_done | not_yet | unsure`) and short,
  kid-friendly feedback ("Almost! The pillows are still on the floor."). The kid can retake or submit anyway.
- **Parent flow**: approval screens show the AI verdict, confidence and feedback next to the photos. The AI never
  awards or removes points. Crediting follows `requires_approval`; the only effect is that a `not_yet` verdict stops
  the auto-approve timeout, so a parent must decide.
- **Storage**: `ai_reviews(evidence_id, provider, model, verdict, confidence, feedback, created_at)`.
- **Guardrails**: images downscaled and EXIF-stripped before sending, per-kid rate limit, timeout with graceful
  "couldn't check" fallback, and the prompt instructs the model to talk about the chore, never the child.

## Open questions

- None outstanding.
