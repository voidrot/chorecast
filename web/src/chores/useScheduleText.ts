import { useI18n } from 'vue-i18n'
import { type ScheduleRule, summarizeRule, WEEKDAYS, type Weekday } from '@/chores/schedule'
import { formatDate } from '@/format'

/** Returns a function that renders a schedule rule as a short, localized sentence. */
export function useScheduleText(): (rule: ScheduleRule | undefined) => string {
  const { t, locale } = useI18n()

  const weekdayName = (w: Weekday) =>
    new Intl.DateTimeFormat(locale.value, { weekday: 'short', timeZone: 'UTC' }).format(
      new Date(Date.UTC(2026, 10, 1 + WEEKDAYS.indexOf(w))), // 2026-11-01 is a Sunday
    )

  return (rule) => {
    const s = summarizeRule(rule, {
      weekdayName,
      formatDate: (d) => formatDate(d, undefined, locale.value),
      lastDay: t('schedule.lastDay').toLocaleLowerCase(locale.value),
    })
    const params = { ...s.params }
    if (typeof params.nth === 'number')
      params.nth = t(`schedule.nth.${params.nth}`).toLocaleLowerCase(locale.value)
    return t(s.key, params, Number(params.n ?? 1))
  }
}
