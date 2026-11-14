import { describe, expect, it } from 'vitest'
import {
  buildApplyBody,
  groupState,
  groups,
  initialSelection,
  refsToCreate,
  type StarterChore,
  type StarterPack,
  toggleGroup,
} from '@/chores/starter'

function chore(key: string, over: Partial<StarterChore> = {}): StarterChore {
  return {
    key,
    name: key,
    category: 'kitchen',
    points: 5,
    assignment: 'auto',
    requiresApproval: false,
    evidence: {} as StarterChore['evidence'],
    exists: false,
    recommended: true,
    schedule: { type: 'daily' } as StarterChore['schedule'],
    ...over,
  }
}

const pack: StarterPack = {
  categories: [
    { key: 'kitchen', name: 'Kitchen' },
    { key: 'empty', name: 'Empty' },
    { key: 'bedroom', name: 'Bedroom' },
  ],
  locations: [
    { key: 'kitchen', name: 'Kitchen', exists: false },
    { key: 'bedroom', name: 'Bedroom', exists: true },
    { key: 'garage', name: 'Garage', exists: false },
  ],
  equipment: [
    { key: 'mop', name: 'Mop', exists: false },
    { key: 'vacuum', name: 'Vacuum', exists: true },
    { key: 'broom', name: 'Broom', exists: false },
  ],
  chores: [
    chore('dishes', { location: 'kitchen', equipment: ['mop', 'vacuum'] }),
    chore('bed', { category: 'bedroom', location: 'bedroom' }),
    chore('mop-floor', { location: 'kitchen', equipment: ['mop'], recommended: false }),
    chore('trash', { exists: true, location: 'garage', equipment: ['broom'] }),
  ],
}

const byKey = (keys: string[]) => pack.chores.filter((c) => keys.includes(c.key))

describe('initialSelection', () => {
  it('picks recommended chores that do not exist yet', () => {
    expect([...initialSelection(pack)].sort()).toEqual(['bed', 'dishes'])
  })
})

describe('groups', () => {
  it('follows category order and drops empty categories', () => {
    const g = groups(pack)
    expect(g.map((x) => x.key)).toEqual(['kitchen', 'bedroom'])
    expect(g[0].chores.map((c) => c.key)).toEqual(['dishes', 'mop-floor', 'trash'])
  })
})

describe('groupState', () => {
  const kitchen = byKey(['dishes', 'mop-floor', 'trash'])

  it('ignores chores that already exist', () => {
    expect(groupState(kitchen, new Set(['dishes', 'mop-floor']))).toBe('all')
    expect(groupState(kitchen, new Set(['dishes']))).toBe('some')
    expect(groupState(kitchen, new Set())).toBe('none')
  })

  it('is none when nothing is selectable', () => {
    expect(groupState(byKey(['trash']), new Set(['trash']))).toBe('none')
  })
})

describe('toggleGroup', () => {
  const kitchen = byKey(['dishes', 'mop-floor', 'trash'])

  it('selects every selectable chore, then clears them', () => {
    const start = new Set(['bed'])
    const all = toggleGroup(kitchen, start)
    expect([...all].sort()).toEqual(['bed', 'dishes', 'mop-floor'])
    expect(start).toEqual(new Set(['bed']))
    expect([...toggleGroup(kitchen, all)]).toEqual(['bed'])
  })

  it('selects the rest when partly selected', () => {
    expect(toggleGroup(kitchen, new Set(['dishes'])).has('mop-floor')).toBe(true)
  })
})

describe('refsToCreate', () => {
  it('lists missing refs of selected new chores once, in pack order', () => {
    const r = refsToCreate(pack, new Set(['mop-floor', 'dishes', 'bed']))
    expect(r.locations.map((l) => l.key)).toEqual(['kitchen'])
    expect(r.equipment.map((e) => e.key)).toEqual(['mop'])
  })

  it('leaves out refs of unselected and existing chores', () => {
    const r = refsToCreate(pack, new Set(['bed', 'trash']))
    expect(r.locations).toEqual([])
    expect(r.equipment).toEqual([])
  })
})

describe('buildApplyBody', () => {
  it('drops existing keys and keeps pack order', () => {
    const body = buildApplyBody(pack, new Set(['trash', 'bed', 'dishes']), false)
    expect(body).toEqual({ chores: ['dishes', 'bed'] })
    expect('disabled' in body).toBe(false)
  })

  it('sets disabled only when paused', () => {
    expect(buildApplyBody(pack, new Set(['bed']), true)).toEqual({
      chores: ['bed'],
      disabled: true,
    })
  })
})
