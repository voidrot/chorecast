import { describe, expect, it } from 'vitest'
import {
  AUDIT_ACTIONS,
  type AuditEntry,
  actorLabel,
  auditQuery,
  describe as describeEntry,
  detailLines,
  mergeEntries,
} from '@/audit/log'
import en from '@/locales/en.json'

describe('AUDIT_ACTIONS', () => {
  it('has each action once, with its sentence and filter name', () => {
    expect(new Set(AUDIT_ACTIONS).size).toBe(AUDIT_ACTIONS.length)
    for (const a of AUDIT_ACTIONS) {
      expect(en.audit.actions[a], a).toBeTruthy()
      expect(en.audit.actionNames[a], a).toBeTruthy()
    }
  })
})

const t = (key: string, params?: Record<string, unknown>) =>
  params ? `${key} ${JSON.stringify(params)}` : key
const te = (key: string) => key === 'audit.fields.dayEnd'

function entry(over: Partial<AuditEntry>): AuditEntry {
  return {
    id: 1,
    createdAt: '2026-10-31T12:00:00Z',
    actorKind: 'parent',
    actorId: 1,
    actorName: 'Mom',
    action: 'kid_archived',
    targetKind: 'kid',
    targetId: 7,
    summary: 'Sam',
    details: {},
    ...over,
  }
}

describe('auditQuery', () => {
  it('maps each filter to its query parameters', () => {
    expect(auditQuery({ actor: null, action: null, range: null })).toEqual({})
    expect(
      auditQuery({ actor: 3, action: 'kid_archived', range: ['2026-10-01', '2026-10-31'] }, 'c1'),
    ).toEqual({
      cursor: 'c1',
      actorKind: 'parent',
      actorId: 3,
      action: 'kid_archived',
      from: '2026-10-01',
      to: '2026-10-31',
    })
    expect(auditQuery({ actor: 'cli', action: null, range: null })).toEqual({ actorKind: 'cli' })
    expect(auditQuery({ actor: 'system', action: null, range: null })).toEqual({
      actorKind: 'system',
    })
  })
})

describe('mergeEntries', () => {
  it('appends a page without repeating entries', () => {
    const merged = mergeEntries(
      [entry({ id: 3 }), entry({ id: 2 })],
      [entry({ id: 2 }), entry({ id: 1 })],
    )
    expect(merged.map((e) => e.id)).toEqual([3, 2, 1])
  })
})

describe('actorLabel and describe', () => {
  it('names the parent, the system or the CLI', () => {
    expect(actorLabel(entry({}), t)).toBe('Mom')
    expect(
      actorLabel(entry({ actorKind: 'system', actorId: undefined, actorName: undefined }), t),
    ).toBe('audit.actor.system')
    expect(
      actorLabel(entry({ actorKind: 'cli', actorId: undefined, actorName: undefined }), t),
    ).toBe('audit.actor.cli')
  })

  it('names the target, or its id when it had no name', () => {
    expect(describeEntry(entry({}), t)).toBe('audit.actions.kid_archived {"target":"Sam"}')
    expect(describeEntry(entry({ summary: undefined }), t)).toBe(
      'audit.actions.kid_archived {"target":"#7"}',
    )
    expect(
      describeEntry(
        entry({ action: 'settings_updated', summary: undefined, targetId: undefined }),
        t,
      ),
    ).toBe('audit.actions.settings_updated {"target":""}')
  })
})

describe('detailLines', () => {
  it('lists setting changes, with known field labels and empty values marked', () => {
    const e = entry({
      action: 'settings_updated',
      details: {
        changes: [
          { field: 'dayEnd', from: '21:00', to: '20:30' },
          { field: 'autoApproveHours', from: '', to: '24' },
        ],
      },
    })
    expect(detailLines(e, t, te)).toEqual([
      'audit.detail.change {"field":"audit.fields.dayEnd","from":"21:00","to":"20:30"}',
      'audit.detail.change {"field":"autoApproveHours","from":"audit.detail.empty","to":"24"}',
    ])
  })

  it('shows points, reasons, roles and shared devices', () => {
    expect(
      detailLines(
        entry({ action: 'points_reversed', details: { amount: -5, reversesId: 9, note: 'oops' } }),
        t,
        te,
      ),
    ).toEqual([
      'audit.detail.amount {"n":-5}',
      'audit.detail.reverses {"id":9}',
      'audit.detail.note {"text":"oops"}',
    ])
    expect(
      detailLines(
        entry({
          action: 'chore_rejected',
          details: { reasonCode: 'needs_redo', reasonText: 'Pans' },
        }),
        t,
        te,
      ),
    ).toEqual(['review.reasons.needs_redo', 'audit.detail.feedback {"text":"Pans"}'])
    expect(
      detailLines(entry({ action: 'invite_created', details: { role: 'owner' } }), t, te),
    ).toEqual(['audit.detail.role {"role":"session.owner"}'])
    expect(detailLines(entry({ action: 'device_paired', details: {} }), t, te)).toEqual([
      'audit.detail.sharedDevice',
    ])
    expect(detailLines(entry({ action: 'device_paired', details: { kidId: 7 } }), t, te)).toEqual(
      [],
    )
  })
})
