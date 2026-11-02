import { describe, expect, it } from 'vitest'
import {
  actionsFor,
  addDays,
  bucketOf,
  civilToday,
  type Instance,
  isStale,
  planByKid,
  rangeFor,
  reassignTargets,
} from '@/today/plan'

function inst(over: Partial<Instance> & { id: number }): Instance {
  return {
    choreId: 1,
    choreName: `chore ${over.id}`,
    date: '2026-11-01',
    dueAt: '2026-11-01T20:00:00Z',
    status: 'open',
    planned: false,
    ...over,
  }
}

describe('dates', () => {
  it('reads today in the household zone, not UTC', () => {
    const now = new Date('2026-11-01T02:30:00Z')
    expect(civilToday('America/Los_Angeles', now)).toBe('2026-10-31')
    expect(civilToday('Pacific/Auckland', now)).toBe('2026-11-01')
  })

  it('adds days across month ends', () => {
    expect(addDays('2026-10-30', 3)).toBe('2026-11-02')
    expect(rangeFor('2026-11-01')).toEqual({ from: '2026-10-25', to: '2026-11-08' })
  })
})

describe('planByKid', () => {
  const today = '2026-11-01'

  it('buckets by what needs a parent, then day, and orders by day within a bucket', () => {
    const [plan] = planByKid(
      [
        inst({ id: 1, kidId: 1, status: 'missed', date: '2026-10-30' }),
        inst({ id: 2, kidId: 1, status: 'submitted' }),
        inst({ id: 3, kidId: 1 }),
        inst({ id: 4, kidId: 1, date: '2026-11-03', planned: true }),
        inst({ id: 5, kidId: 1, status: 'approved', date: '2026-10-31' }),
      ],
      [{ id: 1 }],
      today,
    )
    const ids = (b: keyof typeof plan.buckets) => plan.buckets[b].map((i) => i.id)
    expect(ids('attention')).toEqual([1, 2])
    expect(ids('today')).toEqual([3])
    expect(ids('upcoming')).toEqual([4])
    expect(ids('earlier')).toEqual([5])
  })

  it('keeps kid order, adds shared and unknown kids, and drops empty groups', () => {
    const plans = planByKid(
      [inst({ id: 1 }), inst({ id: 2, kidId: 9 }), inst({ id: 3, kidId: 2 })],
      [{ id: 1 }, { id: 2 }, { id: 3 }],
      today,
    )
    expect(plans.map((p) => p.key)).toEqual(['kid:2', 'kid:9', 'shared'])
  })

  it('treats a planned instance as upcoming', () => {
    expect(bucketOf(inst({ id: 1, planned: true }), today)).toBe('upcoming')
  })
})

describe('actionsFor', () => {
  it('offers only what the server accepts for each status', () => {
    expect(actionsFor(inst({ id: 1, status: 'submitted' }))).toEqual(['approve', 'reject'])
    expect(actionsFor(inst({ id: 1, status: 'approved' }))).toEqual(['reverse'])
    expect(actionsFor(inst({ id: 1, status: 'missed' }))).toEqual(['excuse'])
    expect(actionsFor(inst({ id: 1, status: 'excused' }))).toEqual([])
    expect(actionsFor(inst({ id: 1, kidId: 4 }))).toEqual(['reassign'])
  })

  it('does not offer reassignment of a shared instance', () => {
    expect(actionsFor(inst({ id: 1 }))).toEqual([])
  })
})

describe('conflicts', () => {
  it('reloads on state conflicts but not on validation failures', () => {
    expect(isStale('instance_closed')).toBe(true)
    expect(isStale('already_approved')).toBe(true)
    expect(isStale('validation_failed')).toBe(false)
    expect(isStale('network_error')).toBe(false)
    expect(isStale(undefined)).toBe(false)
  })

  it('lists every kid but the current assignee as a reassignment target', () => {
    const kids = [{ id: 1 }, { id: 2 }, { id: 3 }]
    expect(reassignTargets(kids, inst({ id: 1, kidId: 2 })).map((k) => k.id)).toEqual([1, 3])
  })
})
