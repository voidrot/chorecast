import { describe, expect, it } from 'vitest'
import {
  BULK_APPROVE_MAX,
  badgeText,
  buildRejection,
  chunk,
  groupByKid,
  mergePage,
  pruneSelection,
  type ReviewItem,
  removableIds,
  removeItems,
  selectionState,
  summarizeApprovals,
  toggleAll,
  toggleSelected,
} from '@/review/queue'

function item(id: number, kidName: string, kidId?: number, completedBy?: number): ReviewItem {
  return {
    kidName,
    steps: [],
    instance: { id, kidId, completedBy, choreName: `chore ${id}` },
  } as ReviewItem
}

describe('groupByKid', () => {
  it('groups by completer, keeping API order and first-seen group order', () => {
    const groups = groupByKid([
      item(1, 'Sam', 1),
      item(2, 'Ana', 2),
      item(3, 'Sam', 1),
      item(4, 'Ana', undefined, 2),
    ])
    expect(groups.map((g) => [g.kidName, g.items.map((i) => i.instance.id)])).toEqual([
      ['Sam', [1, 3]],
      ['Ana', [2, 4]],
    ])
  })

  it('does not merge different kids with the same name', () => {
    expect(groupByKid([item(1, 'Sam', 1), item(2, 'Sam', 2)])).toHaveLength(2)
  })

  it('groups shared items without an id by name', () => {
    expect(groupByKid([item(1, 'Sam'), item(2, 'Sam')])).toHaveLength(1)
  })
})

describe('paging', () => {
  it('skips items already listed', () => {
    const merged = mergePage([item(1, 'a', 1)], [item(1, 'a', 1), item(2, 'a', 1)])
    expect(merged.map((i) => i.instance.id)).toEqual([1, 2])
  })
})

describe('selection', () => {
  const items = [item(1, 'a', 1), item(2, 'a', 1)]

  it('toggles without mutating', () => {
    const s = new Set([1])
    expect([...toggleSelected(s, 2)]).toEqual([1, 2])
    expect([...toggleSelected(s, 1)]).toEqual([])
    expect([...s]).toEqual([1])
  })

  it('reports none, some, all', () => {
    expect(selectionState(new Set(), items)).toBe('none')
    expect(selectionState(new Set([1]), items)).toBe('some')
    expect(selectionState(new Set([1, 2, 9]), items)).toBe('all')
  })

  it('select-all selects everything, then clears', () => {
    const all = toggleAll(new Set([1]), items)
    expect([...all]).toEqual([1, 2])
    expect(toggleAll(all, items).size).toBe(0)
  })

  it('prunes ids that left the list', () => {
    expect([...pruneSelection(new Set([1, 2]), removeItems(items, new Set([2])))]).toEqual([1])
  })
})

describe('summarizeApprovals', () => {
  const summary = summarizeApprovals([
    { instanceId: 1, approved: true },
    { instanceId: 2, approved: false, code: 'instance_not_submitted' },
    { instanceId: 3, approved: false, code: 'internal', message: 'boom' },
    { instanceId: 4, approved: false, code: 'already_approved' },
  ])

  it('separates approved, already decided and failed', () => {
    expect(summary.approved).toEqual([1])
    expect(summary.alreadyDecided).toEqual([2, 4])
    expect(summary.failed).toEqual([{ instanceId: 3, code: 'internal', message: 'boom' }])
  })

  it('removes approved and already-decided items but keeps failures listed', () => {
    expect([...removableIds(summary)]).toEqual([1, 2, 4])
  })
})

describe('chunk', () => {
  it('splits into pieces of at most the limit, keeping order', () => {
    const ids = Array.from({ length: 250 }, (_, i) => i)
    const parts = chunk(ids, BULK_APPROVE_MAX)
    expect(parts.map((p) => p.length)).toEqual([100, 100, 50])
    expect(parts.flat()).toEqual(ids)
  })

  it('returns nothing for an empty list', () => {
    expect(chunk([], BULK_APPROVE_MAX)).toEqual([])
  })
})

describe('badgeText', () => {
  it('hides zero, adds + when more pages exist', () => {
    expect(badgeText(0, false)).toBe('')
    expect(badgeText(7, false)).toBe('7')
    expect(badgeText(50, true)).toBe('50+')
  })
})

describe('buildRejection', () => {
  it('needs a reason or text', () => {
    expect(buildRejection(null, '  ')).toBeUndefined()
    expect(buildRejection('not_done', '')).toEqual({ reason: 'not_done' })
    expect(buildRejection(null, ' redo it ')).toEqual({ text: 'redo it' })
    expect(buildRejection('needs_redo', 'x')).toEqual({ reason: 'needs_redo', text: 'x' })
  })
})
