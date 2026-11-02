import type { components } from '@/api/schema'

export type Instance = components['schemas']['ChoreInstance']
export type Evidence = components['schemas']['InstanceEvidence']
type Kid = components['schemas']['Kid']

/** How many days back the overview reaches, so recent misses stay visible. */
export const LOOKBACK_DAYS = 7
/** How many days ahead it shows. */
export const LOOKAHEAD_DAYS = 7

export type Bucket = 'attention' | 'today' | 'upcoming' | 'earlier'

export const BUCKETS: readonly Bucket[] = ['attention', 'today', 'upcoming', 'earlier']

export function addDays(isoDate: string, days: number): string {
  const [y, m, d] = isoDate.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1, d + days)).toISOString().slice(0, 10)
}

/** Minutes since midnight for an HH:MM day end; "00:00" (and "24:00") is the end of the day. */
function dayEndMinutes(dayEnd: string): number {
  const [h, m] = dayEnd.split(':').map(Number)
  const minutes = h * 60 + m
  return minutes === 0 ? 24 * 60 : minutes
}

/**
 * The household's current day, as the server counts it: the day whose close is still ahead. With
 * an early day end (say 21:00) the evening after the close already belongs to the next date, and
 * at the exact close the next day starts. Wall-clock arithmetic follows DST by itself. Shortcut:
 * on the repeated hour of a fall-back night, a day end inside it is read from the second pass
 * too, where the server would already have closed the day.
 */
export function householdToday(timeZone: string, dayEnd: string, now: Date = new Date()): string {
  const parts = Object.fromEntries(
    new Intl.DateTimeFormat('en-CA', {
      timeZone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    })
      .formatToParts(now)
      .map((p) => [p.type, p.value]),
  )
  const date = `${parts.year}-${parts.month}-${parts.day}`
  const wall = Number(parts.hour) * 60 + Number(parts.minute)
  return wall >= dayEndMinutes(dayEnd) ? addDays(date, 1) : date
}

export function rangeFor(today: string): { from: string; to: string } {
  return { from: addDays(today, -LOOKBACK_DAYS), to: addDays(today, LOOKAHEAD_DAYS) }
}

/**
 * Where an instance sits on the screen: what needs a parent (missed, or waiting for approval)
 * comes first, then today's other chores, forecasts, and finished days. Forecasts follow the
 * server's `planned` flag. An open chore that has not closed is still to do, however old its date
 * (a flexible chore carries the week's first day), so it never lands in "Earlier".
 */
export function bucketOf(i: Instance, today: string): Bucket {
  if (i.status === 'missed' || i.status === 'submitted') return 'attention'
  if (i.planned) return 'upcoming'
  if (i.status === 'open' || i.date === today) return 'today'
  return i.date > today ? 'upcoming' : 'earlier'
}

export interface KidPlan {
  /** 'kid:<id>', or 'shared' for instances nobody owns yet. */
  key: string
  kidId?: number
  buckets: Record<Bucket, Instance[]>
}

function newPlan(key: string, kidId?: number): KidPlan {
  return { key, kidId, buckets: { attention: [], today: [], upcoming: [], earlier: [] } }
}

function byDay(a: Instance, b: Instance): number {
  return a.date.localeCompare(b.date) || (a.slot ?? '').localeCompare(b.slot ?? '') || a.id - b.id
}

/**
 * Groups instances under their kid (in the kids' order, then any unknown kid, such as an archived
 * one), then by bucket. Shared instances, which have no assignee, go in a last group. Groups with
 * nothing to show are left out.
 */
export function planByKid(
  instances: readonly Instance[],
  kids: readonly Pick<Kid, 'id'>[],
  today: string,
): KidPlan[] {
  const plans = new Map<number, KidPlan>(kids.map((k) => [k.id, newPlan(`kid:${k.id}`, k.id)]))
  const shared = newPlan('shared')
  for (const i of [...instances].sort(byDay)) {
    let plan = shared
    if (i.kidId !== undefined) {
      plan = plans.get(i.kidId) ?? newPlan(`kid:${i.kidId}`, i.kidId)
      plans.set(i.kidId, plan)
    }
    plan.buckets[bucketOf(i, today)].push(i)
  }
  return [...plans.values(), shared].filter((p) => BUCKETS.some((b) => p.buckets[b].length > 0))
}

export type Action = 'approve' | 'reject' | 'reverse' | 'excuse' | 'reassign' | 'complete'

/** The parent actions the server accepts for an instance in its current state. */
export function actionsFor(i: Instance): Action[] {
  switch (i.status) {
    case 'submitted':
      return ['approve', 'reject']
    case 'approved':
      return ['reverse']
    case 'missed':
      return ['excuse']
    case 'open': {
      // A forecast can't be completed yet, and a shared instance has no assignee to move.
      const out: Action[] = i.planned ? [] : ['complete']
      if (i.kidId !== undefined) out.push('reassign')
      return out
    }
    default:
      return []
  }
}

/** Evidence exists once a kid has submitted the chore at least once. */
export function hasEvidence(i: Instance): boolean {
  return i.completedAt !== undefined
}

/** Codes meaning the screen's copy of the instance is out of date, so it should reload. */
const STALE_CODES = new Set([
  'instance_not_found',
  'instance_not_open',
  'instance_not_submitted',
  'instance_not_approved',
  'instance_not_missed',
  'instance_closed',
  'instance_planned',
  'instance_shared',
  'instance_taken',
])

export function isStale(code: string | undefined): boolean {
  return code !== undefined && STALE_CODES.has(code)
}

/** Who a parent can mark a shared chore done for: its group members. */
export function completers<K extends Pick<Kid, 'id'>>(kids: readonly K[], i: Instance): K[] {
  const members = new Set(i.groupKidIds ?? [])
  return kids.filter((k) => members.has(k.id))
}

/** Kids a chore can move to: everyone but its current assignee. */
export function reassignTargets<K extends Pick<Kid, 'id'>>(kids: readonly K[], i: Instance): K[] {
  return kids.filter((k) => k.id !== i.kidId)
}
