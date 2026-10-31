import { describe, expect, it } from 'vitest'
import {
  emptyScheduleForm,
  ruleFromScheduleForm,
  type ScheduleRule,
  scheduleFormFromRule,
  scheduleProblems,
  sortMonthDays,
  summarizeRule,
  todayIn,
} from '@/chores/schedule'

const today = '2026-10-31' // a Saturday

describe('schedule form', () => {
  it('defaults to unscheduled, prefilled from today', () => {
    const f = emptyScheduleForm(today)
    expect(f.mode).toBe('none')
    expect([f.startDate, f.weekdays, f.monthDays, f.nthWeekday]).toEqual([
      today,
      ['sat'],
      [31],
      'sat',
    ])
    expect(ruleFromScheduleForm(f)).toBeUndefined()
  })

  it.each<ScheduleRule>([
    { type: 'once', date: '2026-11-08' },
    { type: 'daily', startDate: '2026-11-01', interval: 3 },
    { type: 'daily', startDate: '2026-11-01', endDate: '2026-12-01', interval: 1 },
    { type: 'weekly', startDate: '2026-11-01', interval: 2, weekdays: ['mon', 'sat'] },
    { type: 'monthly', startDate: '2026-11-01', interval: 1, monthDays: [1, 15, -1] },
    {
      type: 'monthly',
      startDate: '2026-11-01',
      interval: 3,
      nthWeekday: { n: -1, weekday: 'fri' },
    },
    { type: 'flexible_weekly', startDate: '2026-11-01', timesPerWeek: 2 },
  ])('round-trips %j', (rule) => {
    expect(ruleFromScheduleForm(scheduleFormFromRule(rule, today))).toEqual(rule)
  })

  it('keeps other types’ input when switching, but only sends what applies', () => {
    const f = scheduleFormFromRule(
      { type: 'weekly', startDate: today, interval: 2, weekdays: ['sat', 'mon'] },
      today,
    )
    f.mode = 'flexible_weekly'
    expect(ruleFromScheduleForm(f)).toEqual({
      type: 'flexible_weekly',
      startDate: today,
      timesPerWeek: 2,
    })
    f.mode = 'weekly'
    expect(ruleFromScheduleForm(f)?.weekdays).toEqual(['mon', 'sat'])
  })

  it('sorts month days with last day at the end', () => {
    expect(sortMonthDays([-1, 15, 1, 15])).toEqual([1, 15, -1])
  })

  it('flags what the server would reject', () => {
    const f = emptyScheduleForm(today)
    expect(scheduleProblems(f)).toEqual([])
    expect(scheduleProblems({ ...f, mode: 'weekly', weekdays: [] })).toEqual([
      'schedule.errors.weekdays',
    ])
    expect(scheduleProblems({ ...f, mode: 'monthly', monthDays: [] })).toEqual([
      'schedule.errors.monthDays',
    ])
    expect(scheduleProblems({ ...f, mode: 'monthly', monthlyBy: 'nth', monthDays: [] })).toEqual([])
    expect(scheduleProblems({ ...f, mode: 'daily', endDate: '2026-10-01' })).toEqual([
      'schedule.errors.endDate',
    ])
    expect(scheduleProblems({ ...f, mode: 'daily', startDate: '' })).toEqual([
      'schedule.errors.startDate',
    ])
    expect(scheduleProblems({ ...f, mode: 'once', date: '' })).toEqual(['schedule.errors.date'])
  })

  it('computes today in a timezone', () => {
    const now = new Date('2026-11-01T03:00:00Z')
    expect(todayIn('America/Chicago', now)).toBe('2026-10-31')
    expect(todayIn('Europe/Berlin', now)).toBe('2026-11-01')
  })
})

describe('rule summaries', () => {
  const labels = {
    weekdayName: (w: string) => w.toUpperCase(),
    formatDate: (d: string) => `<${d}>`,
    lastDay: 'last',
  }
  it.each<[ScheduleRule | undefined, string, Record<string, string | number>]>([
    [undefined, 'schedule.summary.none', {}],
    [{ type: 'once', date: '2026-11-08' }, 'schedule.summary.once', { date: '<2026-11-08>' }],
    [{ type: 'daily', interval: 2 }, 'schedule.summary.daily', { n: 2 }],
    [
      { type: 'weekly', weekdays: ['mon', 'thu'] },
      'schedule.summary.weekly',
      { n: 1, days: 'MON, THU' },
    ],
    [
      { type: 'monthly', monthDays: [1, -1] },
      'schedule.summary.monthlyDays',
      { n: 1, days: '1, last' },
    ],
    [
      { type: 'monthly', interval: 2, nthWeekday: { n: 2, weekday: 'tue' } },
      'schedule.summary.monthlyNth',
      { n: 2, nth: 2, day: 'TUE' },
    ],
    [{ type: 'flexible_weekly', timesPerWeek: 3 }, 'schedule.summary.flexible', { n: 3 }],
  ])('summarizes %j', (rule, key, params) => {
    expect(summarizeRule(rule, labels)).toEqual({ key, params })
  })
})
