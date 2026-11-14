import type { components } from '@/api/schema'

export type StarterPack = components['schemas']['StarterPack']
export type StarterChore = components['schemas']['StarterChore']
export type StarterCatalogItem = components['schemas']['StarterCatalogItem']
export type StarterApplyInput = components['schemas']['StarterApplyInput']
export type StarterApplyResult = components['schemas']['StarterApplyResult']

export type GroupState = 'all' | 'some' | 'none'

/** Chores ticked when the screen opens: recommended ones that aren't in the household yet. */
export function initialSelection(pack: StarterPack): Set<string> {
  return new Set(pack.chores.filter((c) => c.recommended && !c.exists).map((c) => c.key))
}

/** Chores per category, in category order; categories with no chores are dropped. */
export function groups(pack: StarterPack): { key: string; name: string; chores: StarterChore[] }[] {
  return pack.categories
    .map((cat) => ({ ...cat, chores: pack.chores.filter((c) => c.category === cat.key) }))
    .filter((g) => g.chores.length > 0)
}

/** Selection state of a group, counting only chores that can still be added. */
export function groupState(chores: StarterChore[], sel: Set<string>): GroupState {
  const selectable = chores.filter((c) => !c.exists)
  const picked = selectable.filter((c) => sel.has(c.key)).length
  if (picked === 0) return 'none'
  return picked === selectable.length ? 'all' : 'some'
}

/** Selects every addable chore of the group, or clears them when all are already selected. */
export function toggleGroup(chores: StarterChore[], sel: Set<string>): Set<string> {
  const next = new Set(sel)
  const clear = groupState(chores, sel) === 'all'
  for (const c of chores) {
    if (c.exists) continue
    if (clear) next.delete(c.key)
    else next.add(c.key)
  }
  return next
}

function selectedNew(pack: StarterPack, sel: Set<string>): StarterChore[] {
  return pack.chores.filter((c) => sel.has(c.key) && !c.exists)
}

/** Locations and equipment that applying the selection will create, each once, in pack order. */
export function refsToCreate(
  pack: StarterPack,
  sel: Set<string>,
): { locations: StarterCatalogItem[]; equipment: StarterCatalogItem[] } {
  const chores = selectedNew(pack, sel)
  const locationKeys = new Set(chores.flatMap((c) => (c.location ? [c.location] : [])))
  const equipmentKeys = new Set(chores.flatMap((c) => c.equipment ?? []))
  return {
    locations: pack.locations.filter((l) => !l.exists && locationKeys.has(l.key)),
    equipment: pack.equipment.filter((e) => !e.exists && equipmentKeys.has(e.key)),
  }
}

export function buildApplyBody(
  pack: StarterPack,
  sel: Set<string>,
  paused: boolean,
): StarterApplyInput {
  const body: StarterApplyInput = { chores: selectedNew(pack, sel).map((c) => c.key) }
  if (paused) body.disabled = true
  return body
}
