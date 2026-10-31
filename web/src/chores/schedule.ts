import type { components } from '@/api/schema'

export type ScheduleRule = components['schemas']['ScheduleRule']
export type ScheduleOccurrence = components['schemas']['ScheduleOccurrence']
export type Weekday = NonNullable<ScheduleRule['weekdays']>[number]

/** Rule weekdays, Sunday first (index = JS getDay()). */
export const WEEKDAYS: Weekday[] = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat']

/** What the editor offers: a rule type, or not scheduled at all. */
export type ScheduleMode = 'none' | ScheduleRule['type']

/** The editor's state. Every type's fields are kept so switching types doesn't lose input. */
export interface ScheduleForm {
  mode: ScheduleMode
  date: string
  startDate: string
  endDate: string | null
  interval: number
  weekdays: Weekday[]
  monthlyBy: 'days' | 'nth'
  monthDays: number[]
  nthN: number
  nthWeekday: Weekday
  timesPerWeek: number
}

/** Today's civil date (YYYY-MM-DD) in the given timezone. */
export function todayIn(timeZone: string, now: Date = new Date()): string {
  // en-CA formats as YYYY-MM-DD.
  return new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(now)
}

function weekdayOf(isoDate: string): Weekday {
  const [y, m, d] = isoDate.split('-').map(Number)
  return WEEKDAYS[new Date(Date.UTC(y, m - 1, d)).getUTCDay()]
}

export function emptyScheduleForm(today: string): ScheduleForm {
  return {
    mode: 'none',
    date: today,
    startDate: today,
    endDate: null,
    interval: 1,
    weekdays: [weekdayOf(today)],
    monthlyBy: 'days',
    monthDays: [Number(today.slice(8, 10))],
    nthN: 1,
    nthWeekday: weekdayOf(today),
    timesPerWeek: 2,
  }
}

export function scheduleFormFromRule(rule: ScheduleRule | undefined, today: string): ScheduleForm {
  const f = emptyScheduleForm(today)
  if (!rule) return f
  f.mode = rule.type
  if (rule.date) f.date = rule.date
  if (rule.startDate) f.startDate = rule.startDate
  f.endDate = rule.endDate || null
  if (rule.interval) f.interval = rule.interval
  if (rule.weekdays?.length) f.weekdays = [...rule.weekdays]
  if (rule.monthDays?.length) f.monthDays = [...rule.monthDays]
  if (rule.nthWeekday) {
    f.monthlyBy = 'nth'
    f.nthN = rule.nthWeekday.n
    f.nthWeekday = rule.nthWeekday.weekday
  }
  if (rule.timesPerWeek) f.timesPerWeek = rule.timesPerWeek
  return f
}

/** The rule to save, or undefined when the chore isn't scheduled. */
export function ruleFromScheduleForm(f: ScheduleForm): ScheduleRule | undefined {
  if (f.mode === 'none') return undefined
  if (f.mode === 'once') return { type: 'once', date: f.date }
  const base: ScheduleRule = {
    type: f.mode,
    startDate: f.startDate,
    ...(f.endDate ? { endDate: f.endDate } : {}),
  }
  switch (f.mode) {
    case 'daily':
      return { ...base, interval: f.interval }
    case 'weekly':
      return {
        ...base,
        interval: f.interval,
        weekdays: WEEKDAYS.filter((w) => f.weekdays.includes(w)),
      }
    case 'monthly':
      return f.monthlyBy === 'nth'
        ? { ...base, interval: f.interval, nthWeekday: { n: f.nthN, weekday: f.nthWeekday } }
        : { ...base, interval: f.interval, monthDays: sortMonthDays(f.monthDays) }
    case 'flexible_weekly':
      return { ...base, timesPerWeek: f.timesPerWeek }
  }
}

/** 1..31 ascending, then -1 (last day). */
export function sortMonthDays(days: number[]): number[] {
  return [...new Set(days)].sort((a, b) => (a === -1 ? 1 : b === -1 ? -1 : a - b))
}

/** i18n keys for problems the server would also reject; empty when the rule may be saved. */
export function scheduleProblems(f: ScheduleForm): string[] {
  const out: string[] = []
  if (f.mode === 'none') return out
  if (f.mode === 'once') {
    if (!f.date) out.push('schedule.errors.date')
    return out
  }
  if (!f.startDate) out.push('schedule.errors.startDate')
  else if (f.endDate && f.endDate < f.startDate) out.push('schedule.errors.endDate')
  if (f.mode === 'weekly' && !f.weekdays.length) out.push('schedule.errors.weekdays')
  if (f.mode === 'monthly' && f.monthlyBy === 'days' && !f.monthDays.length)
    out.push('schedule.errors.monthDays')
  return out
}

/** A translatable one-line description of a rule, for chore lists. */
export interface Summary {
  key: string
  params: Record<string, string | number>
}

export interface SummaryLabels {
  weekdayName: (w: Weekday) => string
  formatDate: (iso: string) => string
  lastDay: string
}

export function summarizeRule(
  rule: ScheduleRule | undefined,
  { weekdayName, formatDate, lastDay }: SummaryLabels,
): Summary {
  if (!rule) return { key: 'schedule.summary.none', params: {} }
  const n = rule.interval ?? 1
  switch (rule.type) {
    case 'once':
      return { key: 'schedule.summary.once', params: { date: formatDate(rule.date ?? '') } }
    case 'daily':
      return { key: 'schedule.summary.daily', params: { n } }
    case 'weekly':
      return {
        key: 'schedule.summary.weekly',
        params: { n, days: (rule.weekdays ?? []).map(weekdayName).join(', ') },
      }
    case 'monthly':
      if (rule.nthWeekday)
        return {
          key: 'schedule.summary.monthlyNth',
          params: {
            n,
            nth: rule.nthWeekday.n,
            day: weekdayName(rule.nthWeekday.weekday),
          },
        }
      return {
        key: 'schedule.summary.monthlyDays',
        params: {
          n,
          days: (rule.monthDays ?? []).map((d) => (d === -1 ? lastDay : String(d))).join(', '),
        },
      }
    case 'flexible_weekly':
      return { key: 'schedule.summary.flexible', params: { n: rule.timesPerWeek ?? 1 } }
  }
}
