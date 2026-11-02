import type { components } from '@/api/schema'

export type PointsEntry = components['schemas']['PointsEntry']
export type AdjustmentBody = components['schemas']['PointsAdjustmentInput']

/** The server's limits on an adjustment (`points.go`). */
export const NOTE_MAX = 500
export const AMOUNT_MAX = 1_000_000

/** Appends a page, skipping entries already listed (a new entry shifts later pages' cursors). */
export function mergePage(existing: readonly PointsEntry[], page: readonly PointsEntry[]) {
  const seen = new Set(existing.map((e) => e.id))
  return [...existing, ...page.filter((e) => !seen.has(e.id))]
}

/** True when the ledger recorded less than was asked for: a deduction clamped to the balance. */
export function isClamped(e: PointsEntry): boolean {
  return e.requestedAmount !== undefined && e.requestedAmount !== e.amount
}

/** An entry can be reversed once; a reversal can itself be reversed, to re-apply the original. */
export function canReverse(e: PointsEntry): boolean {
  return e.reversedById === undefined
}

export type AdjustmentResult = { body: AdjustmentBody } | { problem: string }

/**
 * Validates the adjustment form. `sign` picks add or subtract, so the amount field holds a plain
 * positive number. A problem is an i18n key under `points.adjust`.
 */
export function buildAdjustment(
  sign: 'add' | 'subtract',
  amount: number | null,
  note: string,
): AdjustmentResult {
  if (amount === null || !Number.isInteger(amount) || amount < 1 || amount > AMOUNT_MAX) {
    return { problem: 'amountInvalid' }
  }
  const trimmed = note.trim()
  if (!trimmed) return { problem: 'noteRequired' }
  if (trimmed.length > NOTE_MAX) return { problem: 'noteTooLong' }
  return { body: { amount: sign === 'add' ? amount : -amount, note: trimmed } }
}

/** Validates a reversal's note; returns the trimmed note, or an i18n key under `points.reverse`. */
export function buildReversalNote(note: string): { note: string } | { problem: string } {
  const trimmed = note.trim()
  if (!trimmed) return { problem: 'noteRequired' }
  if (trimmed.length > NOTE_MAX) return { problem: 'noteTooLong' }
  return { note: trimmed }
}
