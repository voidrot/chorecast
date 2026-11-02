import { describe, expect, it } from 'vitest'
import {
  householdBody,
  householdFieldErrors,
  milestoneProblem,
  milestoneRowErrors,
  nextMilestoneDays,
  sortMilestones,
} from '@/settings/form'

describe('household settings form', () => {
  it('maps server field errors onto controls', () => {
    expect(
      householdFieldErrors([
        { location: 'body.streakMilestones', message: 'too many' },
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

  it('flags the milestone row the server rejected', () => {
    expect(
      milestoneRowErrors([
        { location: 'body.streakMilestones[2].days', message: 'bad' },
        { location: 'body.streakMilestones[0]', message: 'bad' },
        { location: 'body.name', message: 'ignored' },
      ]).sort(),
    ).toEqual([0, 2])
    expect(householdFieldErrors([{ location: 'body.streakMilestones[1].days' }])).toEqual({})
  })

  it('picks the next free day and clamps it to 3650', () => {
    expect(nextMilestoneDays([])).toBe(7)
    expect(nextMilestoneDays([{ days: 7, points: 1 }])).toBe(14)
    expect(nextMilestoneDays([{ days: 3650, points: 1 }])).toBe(3649)
    expect(
      nextMilestoneDays([
        { days: 3649, points: 1 },
        { days: 3650, points: 1 },
      ]),
    ).toBe(3648)
  })

  it('always sends milestones, an empty list as []', () => {
    const form = {
      name: 'H',
      timezone: 'UTC',
      dayEnd: '00:00',
      weekStart: 'monday',
      allowNegativeBalance: false,
      siblingVisibility: 'private',
      mediaRetentionDays: 30,
      offlineGraceMinutes: 120,
      defaultChoreMinutes: 10,
      streakMilestones: [{ days: 3, points: 5 }],
      autoApproveHours: 5,
    } as Parameters<typeof householdBody>[0]
    const cleared = householdBody(form, [], null)
    expect(cleared.streakMilestones).toEqual([])
    expect(cleared.autoApproveHours).toBeUndefined()
    const sorted = householdBody(
      form,
      [
        { days: 9, points: 1 },
        { days: 2, points: 1 },
      ],
      12,
    )
    expect(sorted.streakMilestones?.map((m) => m.days)).toEqual([2, 9])
    expect(sorted.autoApproveHours).toBe(12)
  })
})
