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

/** Today's civil date (YYYY-MM-DD) in the given IANA time zone. */
export function civilToday(timeZone: string, now: Date = new Date()): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(now)
}

export function addDays(isoDate: string, days: number): string {
  const [y, m, d] = isoDate.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1, d + days)).toISOString().slice(0, 10)
}

export function rangeFor(today: string): { from: string; to: string } {
  return { from: addDays(today, -LOOKBACK_DAYS), to: addDays(today, LOOKAHEAD_DAYS) }
}

/**
 * Where an instance sits on the screen: what needs a parent (missed, or waiting for approval)
 * comes first, then today's other chores, forecasts, and finished days.
 */
export function bucketOf(i: Instance, today: string): Bucket {
  if (i.status === 'missed' || i.status === 'submitted') return 'attention'
  if (i.planned || i.date > today) return 'upcoming'
  return i.date === today ? 'today' : 'earlier'
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

export type Action = 'approve' | 'reject' | 'reverse' | 'excuse' | 'reassign'

/** The parent actions the server accepts for an instance in its current state. */
export function actionsFor(i: Instance): Action[] {
  switch (i.status) {
    case 'submitted':
      return ['approve', 'reject']
    case 'approved':
      return ['reverse']
    case 'missed':
      return ['excuse']
    case 'open':
      // A shared instance has no assignee to move.
      return i.kidId === undefined ? [] : ['reassign']
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
  'instance_shared',
  'instance_taken',
  'already_approved',
  'already_rejected',
  'already_excused',
])

export function isStale(code: string | undefined): boolean {
  return code !== undefined && STALE_CODES.has(code)
}

/** Kids a chore can move to: everyone but its current assignee. */
export function reassignTargets<K extends Pick<Kid, 'id'>>(kids: readonly K[], i: Instance): K[] {
  return kids.filter((k) => k.id !== i.kidId)
}
