import { ref } from 'vue'

/**
 * The household's IANA timezone. Dates are shown in household time, not the browser's, so
 * parents in different places see the same day boundaries. Defaults to the browser's zone until
 * household settings are loaded.
 */
export const householdTimeZone = ref(Intl.DateTimeFormat().resolvedOptions().timeZone)

export function formatDateTime(
  value: Date | string | number,
  options: Intl.DateTimeFormatOptions = { dateStyle: 'medium', timeStyle: 'short' },
  locale?: string,
  timeZone: string = householdTimeZone.value,
): string {
  return new Intl.DateTimeFormat(locale, { ...options, timeZone }).format(new Date(value))
}

/** Formats a civil date (YYYY-MM-DD, as the API sends dates) without shifting it by timezone. */
export function formatDate(
  isoDate: string,
  options: Intl.DateTimeFormatOptions = { dateStyle: 'medium' },
  locale?: string,
): string {
  const [y, m, d] = isoDate.split('-').map(Number)
  return new Intl.DateTimeFormat(locale, { ...options, timeZone: 'UTC' }).format(
    new Date(Date.UTC(y, m - 1, d)),
  )
}
