import { describe, expect, it } from 'vitest'
import { formatDate, formatDateTime } from '@/format'
import { i18n } from '@/i18n'

describe('formatting', () => {
  it('shows instants in the household timezone', () => {
    const instant = '2026-11-01T05:30:00Z' // 01:30 EDT, the first of two on fall-back day
    expect(formatDateTime(instant, { timeStyle: 'short' }, 'en-US', 'America/New_York')).toBe(
      '1:30 AM',
    )
    expect(formatDateTime(instant, { timeStyle: 'short' }, 'en-US', 'Asia/Kathmandu')).toBe(
      '11:15 AM',
    )
  })
  it('never shifts civil dates', () => {
    expect(formatDate('2026-03-08', { dateStyle: 'medium' }, 'en-US')).toBe('Mar 8, 2026')
  })
  it('resolves messages', () => {
    expect(i18n.global.t('home.version', { version: '1.2.3' })).toBe('Version 1.2.3')
  })
})
