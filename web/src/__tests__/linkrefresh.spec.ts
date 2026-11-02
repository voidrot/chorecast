import { describe, expect, it } from 'vitest'
import { earliestExpiry, refreshDelay } from '@/composables/useLinkRefresh'

describe('signed link refresh', () => {
  it('picks the earliest valid expiry', () => {
    expect(earliestExpiry([])).toBeUndefined()
    expect(earliestExpiry([undefined, 'nope'])).toBeUndefined()
    expect(earliestExpiry(['2026-01-01T00:10:00Z', undefined, '2026-01-01T00:05:00Z'])).toBe(
      Date.parse('2026-01-01T00:05:00Z'),
    )
  })

  it('refetches before the link expires, and at once when it already has', () => {
    const exp = Date.parse('2026-01-01T00:10:00Z')
    const start = exp - 10 * 60_000
    expect(refreshDelay(exp, start)).toBe(9.5 * 60_000)
    expect(refreshDelay(exp, exp - 10_000)).toBe(0)
    expect(refreshDelay(exp, exp + 60_000)).toBe(0)
  })
})
