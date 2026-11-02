import { describe, expect, it } from 'vitest'
import {
  actionsFor,
  addDays,
  bucketOf,
  completers,
  householdToday,
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

describe('householdToday', () => {
  it('is the calendar date for a midnight day end, in the household zone not UTC', () => {
    const now = new Date('2026-11-01T02:30:00Z')
    expect(householdToday('America/Los_Angeles', '00:00', now)).toBe('2026-10-31')
    expect(householdToday('Pacific/Auckland', '00:00', now)).toBe('2026-11-01')
  })

  it('rolls over at an early day end: the evening after 21:00 is already tomorrow', () => {
    const at = (iso: string) => householdToday('UTC', '21:00', new Date(iso))
    expect(at('2026-11-01T20:59:00Z')).toBe('2026-11-01')
    expect(at('2026-11-01T21:00:00Z')).toBe('2026-11-02')
    expect(at('2026-11-01T23:30:00Z')).toBe('2026-11-02')
    expect(at('2026-11-02T00:00:00Z')).toBe('2026-11-02')
  })

  it('follows a spring-forward day', () => {
    // New York, 2026-03-08: 02:00 EST jumps to 03:00 EDT.
    const ny = (dayEnd: string, iso: string) =>
      householdToday('America/New_York', dayEnd, new Date(iso))
    expect(ny('00:00', '2026-03-09T03:59:00Z')).toBe('2026-03-08')
    expect(ny('00:00', '2026-03-09T04:00:00Z')).toBe('2026-03-09')
    // A day end inside the skipped hour closes the day when the gap ends.
    expect(ny('02:30', '2026-03-08T06:59:00Z')).toBe('2026-03-08')
    expect(ny('02:30', '2026-03-08T07:00:00Z')).toBe('2026-03-09')
  })

  it('follows a fall-back day', () => {
    // New York, 2026-11-01: 02:00 EDT falls back to 01:00 EST, a 25-hour day.
    const ny = (iso: string) => householdToday('America/New_York', '00:00', new Date(iso))
    expect(ny('2026-11-02T04:59:00Z')).toBe('2026-11-01')
    expect(ny('2026-11-02T05:00:00Z')).toBe('2026-11-02')
  })
})

describe('dates', () => {
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

  it('follows the server planned flag for forecasts', () => {
    expect(bucketOf(inst({ id: 1, planned: true }), today)).toBe('upcoming')
    // Dated after "today" as the browser sees it, but the server says it is not planned yet.
    expect(bucketOf(inst({ id: 1, date: '2026-11-02', planned: false }), '2026-11-02')).toBe(
      'today',
    )
  })

  it('keeps an open chore that has not closed out of Earlier', () => {
    // A flexible chore carries the week's first day, which can be days back.
    const flexible = inst({ id: 1, date: '2026-10-27', flexible: true })
    expect(bucketOf(flexible, today)).toBe('today')
    expect(bucketOf(inst({ id: 2, date: '2026-10-27', status: 'approved' }), today)).toBe('earlier')
  })
})

describe('actionsFor', () => {
  it('offers only what the server accepts for each status', () => {
    expect(actionsFor(inst({ id: 1, status: 'submitted' }))).toEqual(['approve', 'reject'])
    expect(actionsFor(inst({ id: 1, status: 'approved' }))).toEqual(['reverse'])
    expect(actionsFor(inst({ id: 1, status: 'missed' }))).toEqual(['excuse'])
    expect(actionsFor(inst({ id: 1, status: 'excused' }))).toEqual([])
    expect(actionsFor(inst({ id: 1, kidId: 4 }))).toEqual(['complete', 'reassign'])
  })

  it('offers a shared instance completion but not reassignment', () => {
    expect(actionsFor(inst({ id: 1 }))).toEqual(['complete'])
  })

  it('offers nothing for a forecast that is open, except moving it', () => {
    expect(actionsFor(inst({ id: 1, kidId: 4, planned: true }))).toEqual(['reassign'])
    expect(actionsFor(inst({ id: 1, planned: true }))).toEqual([])
  })
})

describe('conflicts', () => {
  it('reloads on state conflicts but not on validation failures', () => {
    expect(isStale('instance_closed')).toBe(true)
    expect(isStale('instance_not_submitted')).toBe(true)
    expect(isStale('already_approved')).toBe(false)
    expect(isStale('validation_failed')).toBe(false)
    expect(isStale('network_error')).toBe(false)
    expect(isStale(undefined)).toBe(false)
  })

  it('limits who completes a shared chore to its group', () => {
    const kids = [{ id: 1 }, { id: 2 }, { id: 3 }]
    expect(completers(kids, inst({ id: 1, groupKidIds: [1, 3] })).map((k) => k.id)).toEqual([1, 3])
    expect(completers(kids, inst({ id: 1, groupKidIds: null }))).toEqual([])
  })

  it('lists every kid but the current assignee as a reassignment target', () => {
    const kids = [{ id: 1 }, { id: 2 }, { id: 3 }]
    expect(reassignTargets(kids, inst({ id: 1, kidId: 2 })).map((k) => k.id)).toEqual([1, 3])
  })
})
