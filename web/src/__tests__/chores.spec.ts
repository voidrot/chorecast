import { describe, expect, it } from 'vitest'
import {
  type Chore,
  choreProblems,
  emptyChoreForm,
  formFromChore,
  inputFromForm,
} from '@/chores/form'

const chore: Chore = {
  id: 1,
  name: 'Feed the dog',
  description: 'Half a scoop',
  points: 5,
  penalty: 2,
  estimatedMinutes: 5,
  minAge: 9,
  steps: ['Scoop food', 'Fresh water'],
  slots: ['07:00', '18:00'],
  requiresApproval: true,
  escalate: true,
  autoApproveHours: 0,
  evidence: { photos: 1, checklist: true },
  locationId: 3,
  equipmentIds: [4],
  eligibility: [{ kidId: 7, allow: true }],
  createdAt: '2026-10-31T12:00:00Z',
  updatedAt: '2026-10-31T12:00:00Z',
}

describe('chore form', () => {
  it('round-trips a chore', () => {
    const { id: _, createdAt: _c, updatedAt: _u, ...input } = chore
    expect(inputFromForm(formFromChore(chore))).toEqual(input)
  })

  it('maps the three auto-approve modes', () => {
    expect(formFromChore({ ...chore, autoApproveHours: undefined }).autoApprove).toBe('household')
    expect(formFromChore(chore).autoApprove).toBe('never')
    const f = formFromChore({ ...chore, autoApproveHours: 12 })
    expect([f.autoApprove, f.autoApproveHours]).toEqual(['hours', 12])
    expect(inputFromForm({ ...f, autoApprove: 'household' }).autoApproveHours).toBeUndefined()
  })

  it('drops empty steps, sorts and dedupes slots, and leaves out empty fields', () => {
    const f = {
      ...emptyChoreForm(),
      name: ' Sweep ',
      steps: [' ', 'Hall'],
      slots: ['18:00', '07:00', '18:00'],
    }
    expect(inputFromForm(f)).toEqual({
      name: 'Sweep',
      points: 5,
      requiresApproval: true,
      evidence: { photos: 0 },
      steps: ['Hall'],
      slots: ['07:00', '18:00'],
    })
  })

  it('flags what the server would reject', () => {
    expect(
      choreProblems({
        ...emptyChoreForm(),
        name: 'x',
        beforeAfter: true,
        photos: 1,
        checklist: true,
      }),
    ).toEqual(['chores.errors.beforeAfter', 'chores.errors.checklist'])
    expect(choreProblems({ ...emptyChoreForm(), name: 'x' })).toEqual([])
  })
})
