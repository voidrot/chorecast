import { describe, expect, it } from 'vitest'
import { householdFieldErrors, milestoneProblem, sortMilestones } from '@/settings/form'

describe('household settings form', () => {
  it('maps server field errors onto controls', () => {
    expect(
      householdFieldErrors([
        { location: 'body.streakMilestones[1].days', message: 'bad' },
        { location: 'body.mediaRetentionDays', message: 'bad' },
        { location: 'body.other', message: 'ignored' },
      ]),
    ).toEqual({ streakMilestones: true, mediaRetentionDays: true })
    expect(householdFieldErrors(undefined)).toEqual({})
  })

  it('sorts milestones without mutating', () => {
    const list = [
      { days: 30, points: 5 },
      { days: 3, points: 1 },
    ]
    expect(sortMilestones(list).map((m) => m.days)).toEqual([3, 30])
    expect(list[0]?.days).toBe(30)
  })

  it('rejects invalid milestone lists', () => {
    expect(milestoneProblem([{ days: 3, points: 1 }])).toBeUndefined()
    expect(milestoneProblem([])).toBeUndefined()
    expect(milestoneProblem([{ days: 0, points: 1 }])).toBe('settings.milestoneDays')
    expect(milestoneProblem([{ days: 3, points: 0 }])).toBe('settings.milestonePoints')
    expect(
      milestoneProblem([
        { days: 3, points: 1 },
        { days: 3, points: 2 },
      ]),
    ).toBe('settings.milestoneDuplicate')
    expect(
      milestoneProblem(Array.from({ length: 21 }, (_, i) => ({ days: i + 1, points: 1 }))),
    ).toBe('settings.milestoneMax')
  })
})
