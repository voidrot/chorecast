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
| Negative balance | **Household setting `allow_negative_balance`.** When off, a deduction is clamped to the current balance (the entry records the clamp). |
| Corrections | **Append-only ledger.** Mistakes are fixed with a reversing entry linked to the original; nothing is edited or deleted. |
| Manual adjustments | Parent can add or subtract points with a **required note**. **Recurring adjustments** (e.g. weekly base allowance): per kid, amount + note + schedule rule (same model as chores), pausable; writes `adjustment` entries. |
| Transfers | Kids can gift points to a sibling; **parent approves**. |
| Behavior | Parents log positive and negative behaviors, from presets or ad hoc, with point values. Goals, check-ins and behavior streaks add bonuses. See [behavior.md](behavior.md). |
| Streaks | **Daily streak**: every required (non-bonus) chore done that day, excused ones don't break it, bonus points at household milestones (e.g. 3/7/30 days). **Per-chore streaks** for recurring chores. |
| Bonus chores | Optional chores any eligible kid can **claim** (first claim wins) for extra points. No penalty if unclaimed. |
| Rewards | Kid **requests**, points are **held**, parent approves (deducted) or denies (hold released). Rewards can opt in to **needs delivery**: `approved → delivered`, with a parent "to deliver" list. |
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
- An entry can be reversed at most once (`UNIQUE(reverses_id)`); a reversal's amount is the negated original.
- **Holds** are not ledger entries. A pending reward redemption or transfer request reserves points:
  `available = balance − SUM(cost of pending redemptions and transfers)`. A request is allowed only if
  `available ≥ cost`, regardless of `allow_negative_balance`.
- Approving a redemption writes one `reward` entry (−cost). Approving a transfer writes `transfer_out` and
  `transfer_in` with the same `transfer_id` in one transaction.
- Un-approving a chore, un-excusing, or removing a behavior all write `reversal` entries.

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

## Streaks

Evaluated at day close for each kid:
- **Daily streak**: the day counts if the kid had at least one required chore and every required chore is
  `approved`, `submitted` or `excused`. `submitted` counts provisionally; a later rejection doesn't retro-break the streak (decide in implementation if that proves abusable).
- Milestone bonuses are household-configured (`[{days: 3, points: 5}, {days: 7, points: 15}]`) and written as
  `streak_bonus` entries.
- **Per-chore streaks** are computed from instance history, display only for v1 (no bonus).

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
