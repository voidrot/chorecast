import { describe, expect, it } from 'vitest'
import {
  assignmentFieldErrors,
  assignmentFormFromApi,
  assignmentFromForm,
  assignmentProblems,
  choreProblems,
  emptyAssignmentForm,
  emptyChoreForm,
  moveKid,
  resetsRotation,
} from '@/chores/form'

const rotation = (kidIds: number[]) => ({
  ...emptyAssignmentForm(),
  mode: 'rotation' as const,
  kidIds,
})

describe('assignment form', () => {
  it('treats a missing assignment as everyone and sends no kids for it', () => {
    const a = assignmentFormFromApi(undefined)
    expect(a.mode).toBe('everyone')
    expect(assignmentFromForm({ ...a, kidIds: [1, 2] })).toEqual({ mode: 'everyone' })
  })

  it('keeps the kid order and sends shared options only for shared', () => {
    expect(assignmentFromForm(rotation([8, 7]))).toEqual({ mode: 'rotation', kidIds: [8, 7] })
    const shared = { ...emptyAssignmentForm(), mode: 'shared' as const, kidIds: [1, 2] }
    expect(
      assignmentFromForm({ ...shared, sharedPoints: 'split_evenly', sharedPenalty: 'none' }),
    ).toEqual({
      mode: 'shared',
      kidIds: [1, 2],
      sharedPoints: 'split_evenly',
      sharedPenalty: 'none',
    })
  })

  it('round-trips a shared assignment through the form', () => {
    const a = {
      mode: 'shared',
      kidIds: [3, 4],
      sharedPoints: 'split_evenly',
      sharedPenalty: 'none',
    } as const
    expect(assignmentFromForm(assignmentFormFromApi(a))).toEqual(a)
  })

  it('mirrors the server kid minimums', () => {
    const f = emptyAssignmentForm()
    expect(assignmentProblems(f)).toEqual([])
    expect(assignmentProblems({ ...f, mode: 'auto' })).toEqual([])
    expect(assignmentProblems({ ...f, mode: 'fixed' })).toHaveLength(1)
    expect(assignmentProblems(rotation([1]))).toEqual([])
    expect(assignmentProblems({ ...f, mode: 'shared', kidIds: [1] })).toEqual([
      'chores.assignment.errors.sharedKids',
    ])
    expect(assignmentProblems({ ...f, mode: 'shared', kidIds: [1, 2] })).toEqual([])
    const bad = { ...emptyChoreForm(), name: 'x', assignment: { ...f, mode: 'fixed' as const } }
    expect(choreProblems(bad)).toContain('chores.assignment.errors.kids')
  })

  it('flags a rotation reset only when the mode or the ordered kids change', () => {
    const orig = rotation([1, 2, 3])
    expect(resetsRotation(orig, rotation([1, 2, 3]))).toBe(false)
    expect(resetsRotation(orig, rotation([2, 1, 3]))).toBe(true)
    expect(resetsRotation(orig, rotation([1, 2]))).toBe(true)
    expect(resetsRotation(orig, { ...orig, mode: 'fixed' })).toBe(true)
    expect(resetsRotation({ ...orig, mode: 'fixed' }, rotation([1]))).toBe(false)
  })

  it('moves a kid within bounds without mutating', () => {
    const ids = [1, 2, 3]
    expect(moveKid(ids, 1, -1)).toEqual([2, 1, 3])
    expect(moveKid(ids, 1, 1)).toEqual([1, 3, 2])
    expect(moveKid(ids, 0, -1)).toBe(ids)
    expect(moveKid(ids, 2, 1)).toBe(ids)
    expect(ids).toEqual([1, 2, 3])
  })

  it('maps server field errors onto the assignment controls', () => {
    expect(
      assignmentFieldErrors([
        { location: 'body.assignment.kidIds[1]', message: 'unknown kid' },
        { location: 'body.assignment.kidIds', message: 'shared needs at least 2 kid(s)' },
        { location: 'body.assignment.sharedPenalty', message: 'bad' },
        { location: 'body.name', message: 'ignored' },
      ]),
    ).toEqual({ kidIds: true, sharedPenalty: true })
    expect(assignmentFieldErrors(undefined)).toEqual({})
  })
})
