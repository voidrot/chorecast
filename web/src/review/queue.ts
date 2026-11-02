import type { components } from '@/api/schema'

export type ReviewItem = components['schemas']['ReviewQueueItem']
export type ReviewPhoto = components['schemas']['EvidencePhoto']
export type ApprovalResult = components['schemas']['ReviewQueueApprovalResult']
export type RejectBody = components['schemas']['RejectChoreInstanceInputBody']
export type RejectReason = NonNullable<RejectBody['reason']>

export const REJECT_REASONS: readonly RejectReason[] = [
  'not_done',
  'not_finished',
  'needs_redo',
  'evidence_unclear',
]

/** The server's limit on a rejection's free text. */
export const REJECT_TEXT_MAX = 500

/** Signed photo links last about 10 minutes; reload before that. */
export const PHOTO_REFRESH_MS = 8 * 60 * 1000

export interface KidGroup {
  key: string
  kidName: string
  items: ReviewItem[]
}

/**
 * Groups the flat, oldest-first queue by kid. Groups appear in the order their oldest item does,
 * and each group keeps the API's order.
 */
export function groupByKid(items: readonly ReviewItem[]): KidGroup[] {
  const groups = new Map<string, KidGroup>()
  for (const item of items) {
    const id = item.instance.completedBy ?? item.instance.kidId
    const key = id === undefined ? `name:${item.kidName}` : `kid:${id}`
    let group = groups.get(key)
    if (!group) {
      group = { key, kidName: item.kidName, items: [] }
      groups.set(key, group)
    }
    group.items.push(item)
  }
  return [...groups.values()]
}

/** Appends a page, skipping instances already listed (a resubmitted chore can reappear). */
export function mergePage(existing: readonly ReviewItem[], page: readonly ReviewItem[]) {
  const seen = new Set(existing.map((i) => i.instance.id))
  return [...existing, ...page.filter((i) => !seen.has(i.instance.id))]
}

export function removeItems(items: readonly ReviewItem[], ids: ReadonlySet<number>) {
  return items.filter((i) => !ids.has(i.instance.id))
}

export function toggleSelected(selected: ReadonlySet<number>, id: number): Set<number> {
  const next = new Set(selected)
  if (!next.delete(id)) next.add(id)
  return next
}

export type SelectionState = 'none' | 'some' | 'all'

export function selectionState(
  selected: ReadonlySet<number>,
  items: readonly ReviewItem[],
): SelectionState {
  const n = items.filter((i) => selected.has(i.instance.id)).length
  if (n === 0) return 'none'
  return n === items.length ? 'all' : 'some'
}

/** Select-all selects every listed item, or clears the selection when all are selected. */
export function toggleAll(
  selected: ReadonlySet<number>,
  items: readonly ReviewItem[],
): Set<number> {
  return selectionState(selected, items) === 'all'
    ? new Set<number>()
    : new Set(items.map((i) => i.instance.id))
}

/** Drops ids that are no longer listed (after a reload or a removal). */
export function pruneSelection(
  selected: ReadonlySet<number>,
  items: readonly ReviewItem[],
): Set<number> {
  const listed = new Set(items.map((i) => i.instance.id))
  return new Set([...selected].filter((id) => listed.has(id)))
}

/** Codes meaning the chore is no longer waiting for approval, so it should leave the list. */
const GONE_CODES = new Set(['already_approved', 'instance_not_submitted', 'instance_not_found'])

export function isGone(code: string | undefined): boolean {
  return code !== undefined && GONE_CODES.has(code)
}

export interface ApprovalFailure {
  instanceId: number
  code: string
  /** The server's text, used when the code has no translation. */
  message: string
}

export interface ApprovalSummary {
  approved: number[]
  /** Not approved by this request, but no longer submitted (someone else decided first). */
  alreadyDecided: number[]
  failed: ApprovalFailure[]
}

export function summarizeApprovals(results: readonly ApprovalResult[]): ApprovalSummary {
  const summary: ApprovalSummary = { approved: [], alreadyDecided: [], failed: [] }
  for (const r of results) {
    if (r.approved) summary.approved.push(r.instanceId)
    else if (isGone(r.code)) summary.alreadyDecided.push(r.instanceId)
    else {
      summary.failed.push({
        instanceId: r.instanceId,
        code: r.code ?? 'error',
        message: r.message ?? '',
      })
    }
  }
  return summary
}

/** Ids to take off the list after a bulk approval. */
export function removableIds(summary: ApprovalSummary): Set<number> {
  return new Set([...summary.approved, ...summary.alreadyDecided])
}

/** The nav badge text: the first page's count, with "+" when more pages exist. Empty for none. */
export function badgeText(count: number, hasMore: boolean): string {
  if (count <= 0) return ''
  return hasMore ? `${count}+` : String(count)
}

/** A rejection needs a reason, text, or both; undefined when it has neither. */
export function buildRejection(reason: RejectReason | null, text: string): RejectBody | undefined {
  const trimmed = text.trim()
  if (!reason && !trimmed) return undefined
  const body: RejectBody = {}
  if (reason) body.reason = reason
  if (trimmed) body.text = trimmed
  return body
}
