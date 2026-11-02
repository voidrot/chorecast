import type { components, operations } from '@/api/schema'

export type AuditEntry = components['schemas']['AuditEntry']
export type AuditAction = AuditEntry['action']
export type AuditQuery = NonNullable<operations['listAuditLog']['parameters']['query']>

/** Every action, in the order the action filter lists them. */
export const AUDIT_ACTIONS: readonly AuditAction[] = [
  'settings_updated',
  'chore_approved',
  'chore_rejected',
  'chore_unapproved',
  'chore_excused',
  'points_adjusted',
  'points_reversed',
  'invite_created',
  'invite_revoked',
  'invite_accepted',
  'parent_role_changed',
  'parent_removed',
  'parent_password_reset',
  'parent_totp_disabled',
  'device_paired',
  'device_revoked',
  'kid_archived',
  'kid_unarchived',
]

/** The actor filter: the system, the CLI, or a parent's id; null for everyone. */
export type ActorFilter = 'system' | 'cli' | number | null

export interface AuditFilters {
  actor: ActorFilter
  action: AuditAction | null
  /** Household-local days, YYYY-MM-DD, both inclusive. */
  range: [string, string] | null
}

/** The GET /audit query for the filters and an optional cursor. */
export function auditQuery(f: AuditFilters, cursor?: string): AuditQuery {
  const q: AuditQuery = {}
  if (cursor) q.cursor = cursor
  if (f.actor === 'system' || f.actor === 'cli') q.actorKind = f.actor
  else if (typeof f.actor === 'number') {
    q.actorKind = 'parent'
    q.actorId = f.actor
  }
  if (f.action) q.action = f.action
  if (f.range) {
    q.from = f.range[0]
    q.to = f.range[1]
  }
  return q
}

/** Appends a page to the list, skipping entries already listed. */
export function mergeEntries(
  list: readonly AuditEntry[],
  page: readonly AuditEntry[],
): AuditEntry[] {
  const seen = new Set(list.map((e) => e.id))
  return [...list, ...page.filter((e) => !seen.has(e.id))]
}

type Translate = (key: string, params?: Record<string, unknown>) => string
type Exists = (key: string) => boolean

/** Who did it: the parent's name, or the system or the CLI. */
export function actorLabel(e: AuditEntry, t: Translate): string {
  if (e.actorKind === 'parent') return e.actorName || t('audit.actor.unknown')
  return t(`audit.actor.${e.actorKind}`)
}

/** What happened, naming the target (by its name at the time, else its id). */
export function describe(e: AuditEntry, t: Translate): string {
  const target = e.summary || (e.targetId === undefined ? '' : `#${e.targetId}`)
  return t(`audit.actions.${e.action}`, { target })
}

/** The details worth showing, one line each. */
export function detailLines(e: AuditEntry, t: Translate, te: Exists): string[] {
  const d = e.details
  const lines: string[] = []
  const empty = t('audit.detail.empty')
  for (const c of d.changes ?? []) {
    const key = `audit.fields.${c.field}`
    lines.push(
      t('audit.detail.change', {
        field: te(key) ? t(key) : c.field,
        from: c.from || empty,
        to: c.to || empty,
      }),
    )
  }
  if (d.amount !== undefined) lines.push(t('audit.detail.amount', { n: d.amount }))
  if (d.reversesId) lines.push(t('audit.detail.reverses', { id: d.reversesId }))
  if (d.reasonCode) lines.push(t(`review.reasons.${d.reasonCode}`))
  if (d.reasonText) lines.push(t('audit.detail.feedback', { text: d.reasonText }))
  if (d.note) lines.push(t('audit.detail.note', { text: d.note }))
  if (d.role) {
    lines.push(
      t('audit.detail.role', {
        role: t(d.role === 'owner' ? 'session.owner' : 'session.parent'),
      }),
    )
  }
  if (e.action === 'device_paired' && !d.kidId) lines.push(t('audit.detail.sharedDevice'))
  return lines
}
