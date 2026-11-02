import { describe, expect, it } from 'vitest'
import {
  buildAdjustment,
  buildReversalNote,
  canReverse,
  isClamped,
  mergePage,
  NOTE_MAX,
  type PointsEntry,
} from '@/points/ledger'

function entry(over: Partial<PointsEntry>): PointsEntry {
  return {
    id: 1,
    kidId: 1,
    kind: 'adjustment',
    amount: 5,
    balanceAfter: 5,
    createdAt: '2026-11-01T10:00:00Z',
    localDate: '2026-11-01',
    ...over,
  }
}

describe('buildAdjustment', () => {
  it('signs the amount by direction and trims the note', () => {
    expect(buildAdjustment('add', 10, '  bonus ')).toEqual({ body: { amount: 10, note: 'bonus' } })
    expect(buildAdjustment('subtract', 10, 'lost toy')).toEqual({
      body: { amount: -10, note: 'lost toy' },
    })
  })

  it('rejects a missing, zero, fractional or oversized amount', () => {
    for (const a of [null, 0, -3, 1.5, 1_000_001]) {
      expect(buildAdjustment('add', a, 'x')).toEqual({ problem: 'amountInvalid' })
    }
  })

  it('requires a note within the limit', () => {
    expect(buildAdjustment('add', 1, '   ')).toEqual({ problem: 'noteRequired' })
    expect(buildAdjustment('add', 1, 'x'.repeat(NOTE_MAX + 1))).toEqual({ problem: 'noteTooLong' })
  })
})

describe('note length', () => {
  it('counts characters, so 500 astral characters fit', () => {
    const note = '😀'.repeat(NOTE_MAX)
    expect(buildAdjustment('add', 1, note)).toEqual({ body: { amount: 1, note } })
    expect(buildReversalNote(`${note}😀`)).toEqual({ problem: 'noteTooLong' })
  })
})

describe('buildReversalNote', () => {
  it('requires a note', () => {
    expect(buildReversalNote(' ')).toEqual({ problem: 'noteRequired' })
    expect(buildReversalNote(' oops ')).toEqual({ note: 'oops' })
  })
})

describe('entry state', () => {
  it('flags a clamped deduction only when the recorded amount differs', () => {
    expect(isClamped(entry({ amount: -3, requestedAmount: -10 }))).toBe(true)
    expect(isClamped(entry({ amount: -3, requestedAmount: -3 }))).toBe(false)
    expect(isClamped(entry({}))).toBe(false)
  })

  it('allows reversing an entry only until it has been reversed', () => {
    expect(canReverse(entry({}))).toBe(true)
    expect(canReverse(entry({ reversedById: 9 }))).toBe(false)
    expect(canReverse(entry({ kind: 'reversal', reversesId: 2 }))).toBe(true)
  })
})

describe('mergePage', () => {
  it('skips entries that a shifted page repeats', () => {
    const merged = mergePage(
      [entry({ id: 3 }), entry({ id: 2 })],
      [entry({ id: 2 }), entry({ id: 1 })],
    )
    expect(merged.map((e) => e.id)).toEqual([3, 2, 1])
  })
})
