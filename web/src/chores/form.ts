import type { components } from '@/api/schema'
import { householdTimeZone } from '@/format'
import {
  emptyScheduleForm,
  ruleFromScheduleForm,
  type ScheduleForm,
  scheduleFormFromRule,
  scheduleProblems,
  todayIn,
} from './schedule'

export type Chore = components['schemas']['Chore']
export type ChoreInput = components['schemas']['ChoreInput']
export type ChoreAssignment = components['schemas']['ChoreAssignment']
export type AssignmentMode = ChoreAssignment['mode']
export type SharedPoints = NonNullable<ChoreAssignment['sharedPoints']>
export type SharedPenalty = NonNullable<ChoreAssignment['sharedPenalty']>

export const ASSIGNMENT_MODES: AssignmentMode[] = [
  'fixed',
  'everyone',
  'rotation',
  'auto',
  'shared',
]

/** Kids the server requires per mode; `auto` takes none (empty = every kid), `everyone` ignores the list. */
const MIN_KIDS: Partial<Record<AssignmentMode, number>> = { fixed: 1, rotation: 1, shared: 2 }

/** `kidIds` is ordered: for rotation, that order is the turn order. */
export interface AssignmentForm {
  mode: AssignmentMode
  kidIds: number[]
  sharedPoints: SharedPoints
  sharedPenalty: SharedPenalty
}

export function emptyAssignmentForm(): AssignmentForm {
  return {
    mode: 'everyone',
    kidIds: [],
    sharedPoints: 'completer_takes_all',
    sharedPenalty: 'all',
  }
}

/** A missing assignment means everyone, as on the server. */
export function assignmentFormFromApi(a?: ChoreAssignment): AssignmentForm {
  return {
    mode: a?.mode ?? 'everyone',
    kidIds: [...(a?.kidIds ?? [])],
    sharedPoints: a?.sharedPoints ?? 'completer_takes_all',
    sharedPenalty: a?.sharedPenalty ?? 'all',
  }
}

/** Sends only what the mode uses; the server drops the rest anyway. */
export function assignmentFromForm(f: AssignmentForm): ChoreAssignment {
  return {
    mode: f.mode,
    ...(f.mode !== 'everyone' && f.kidIds.length ? { kidIds: [...f.kidIds] } : {}),
    ...(f.mode === 'shared'
      ? { sharedPoints: f.sharedPoints, sharedPenalty: f.sharedPenalty }
      : {}),
  }
}

/** i18n keys for assignment problems the server would also reject. */
export function assignmentProblems(f: AssignmentForm): string[] {
  const need = MIN_KIDS[f.mode] ?? 0
  if (f.kidIds.length >= need) return []
  return [
    f.mode === 'shared' ? 'chores.assignment.errors.sharedKids' : 'chores.assignment.errors.kids',
  ]
}

/**
 * Whether saving would reset an existing rotation: the server restarts it from the first kid when the
 * mode or the ordered kid list changes. `original` is the assignment as loaded.
 */
export function resetsRotation(original: AssignmentForm, current: AssignmentForm): boolean {
  if (original.mode !== 'rotation') return false
  return (
    current.mode !== 'rotation' ||
    current.kidIds.length !== original.kidIds.length ||
    current.kidIds.some((id, i) => id !== original.kidIds[i])
  )
}

/** Moves the kid at `index` by `by` places (clamped); returns a new list. */
export function moveKid(kidIds: number[], index: number, by: -1 | 1): number[] {
  const to = index + by
  if (index < 0 || index >= kidIds.length || to < 0 || to >= kidIds.length) return kidIds
  const out = [...kidIds]
  ;[out[index], out[to]] = [out[to] as number, out[index] as number]
  return out
}

export type AssignmentFieldErrors = Partial<
  Record<'mode' | 'kidIds' | 'sharedPoints' | 'sharedPenalty', string>
>

/** Picks the server's 422 messages for the assignment controls out of the problem's field errors. */
export function assignmentFieldErrors(
  errors: { location?: string; message?: string }[] | null | undefined,
): AssignmentFieldErrors {
  const out: AssignmentFieldErrors = {}
  for (const e of errors ?? []) {
    const m = /^body\.assignment\.(mode|kidIds|sharedPoints|sharedPenalty)(?:\[\d+\])?$/.exec(
      e.location ?? '',
    )
    const key = m?.[1] as keyof AssignmentFieldErrors | undefined
    if (key && e.message && !out[key]) out[key] = e.message
  }
  return out
}

/** How a chore auto-approves: like the household, never, or after N hours. */
export type AutoApproveMode = 'household' | 'never' | 'hours'

export interface ChoreForm {
  name: string
  description: string
  points: number
  penalty: number
  estimatedMinutes: number | null
  minAge: number | null
  steps: string[]
  slots: string[]
  schedule: ScheduleForm
  assignment: AssignmentForm
  requiresApproval: boolean
  escalate: boolean
  autoApprove: AutoApproveMode
  autoApproveHours: number
  photos: number
  beforeAfter: boolean
  video: boolean
  checklist: boolean
  note: boolean
  locationId: number | null
  equipmentIds: number[]
  eligibility: { kidId: number; allow: boolean }[]
  disabled: boolean
}

/** today defaults to the household's current date (the schedule's default start). */
export function emptyChoreForm(today = todayIn(householdTimeZone.value)): ChoreForm {
  return {
    name: '',
    description: '',
    points: 5,
    penalty: 0,
    estimatedMinutes: null,
    minAge: null,
    steps: [],
    slots: [],
    schedule: emptyScheduleForm(today),
    assignment: emptyAssignmentForm(),
    requiresApproval: true,
    escalate: false,
    autoApprove: 'household',
    autoApproveHours: 24,
    photos: 0,
    beforeAfter: false,
    video: false,
    checklist: false,
    note: false,
    locationId: null,
    equipmentIds: [],
    eligibility: [],
    disabled: false,
  }
}

export function formFromChore(c: Chore, today = todayIn(householdTimeZone.value)): ChoreForm {
  const hours = c.autoApproveHours
  return {
    name: c.name,
    description: c.description ?? '',
    points: c.points,
    penalty: c.penalty ?? 0,
    estimatedMinutes: c.estimatedMinutes ?? null,
    minAge: c.minAge ?? null,
    steps: [...(c.steps ?? [])],
    slots: [...(c.slots ?? [])],
    schedule: scheduleFormFromRule(c.schedule, today),
    assignment: assignmentFormFromApi(c.assignment),
    requiresApproval: c.requiresApproval,
    escalate: c.escalate ?? false,
    autoApprove: hours == null ? 'household' : hours === 0 ? 'never' : 'hours',
    autoApproveHours: hours && hours > 0 ? hours : 24,
    photos: c.evidence.photos,
    beforeAfter: c.evidence.beforeAfter ?? false,
    video: c.evidence.video ?? false,
    checklist: c.evidence.checklist ?? false,
    note: c.evidence.note ?? false,
    locationId: c.locationId ?? null,
    equipmentIds: [...(c.equipmentIds ?? [])],
    eligibility: (c.eligibility ?? []).map((e) => ({ ...e })),
    disabled: c.disabled ?? false,
  }
}

/** The request body; empty optional fields are left out (the server clears them). */
export function inputFromForm(f: ChoreForm): ChoreInput {
  const steps = f.steps.map((s) => s.trim()).filter(Boolean)
  const schedule = ruleFromScheduleForm(f.schedule)
  return {
    name: f.name.trim(),
    points: f.points,
    requiresApproval: f.requiresApproval,
    evidence: {
      photos: f.photos,
      ...(f.beforeAfter ? { beforeAfter: true } : {}),
      ...(f.video ? { video: true } : {}),
      ...(f.checklist && steps.length ? { checklist: true } : {}),
      ...(f.note ? { note: true } : {}),
    },
    ...(f.description.trim() ? { description: f.description.trim() } : {}),
    ...(f.penalty > 0 ? { penalty: f.penalty } : {}),
    ...(f.estimatedMinutes ? { estimatedMinutes: f.estimatedMinutes } : {}),
    ...(f.minAge ? { minAge: f.minAge } : {}),
    ...(steps.length ? { steps } : {}),
    ...(f.slots.length ? { slots: [...new Set(f.slots)].sort() } : {}),
    ...(schedule ? { schedule } : {}),
    assignment: assignmentFromForm(f.assignment),
    ...(f.escalate ? { escalate: true } : {}),
    ...(f.autoApprove === 'never' ? { autoApproveHours: 0 } : {}),
    ...(f.autoApprove === 'hours' ? { autoApproveHours: f.autoApproveHours } : {}),
    ...(f.locationId != null ? { locationId: f.locationId } : {}),
    ...(f.equipmentIds.length ? { equipmentIds: [...f.equipmentIds] } : {}),
    ...(f.eligibility.length ? { eligibility: f.eligibility.map((e) => ({ ...e })) } : {}),
    ...(f.disabled ? { disabled: true } : {}),
  }
}

/** i18n keys for form problems the server would also reject; empty when it may be saved. */
export function choreProblems(f: ChoreForm): string[] {
  const out: string[] = []
  if (!f.name.trim()) out.push('chores.errors.name')
  if (f.beforeAfter && f.photos < 2) out.push('chores.errors.beforeAfter')
  if (f.checklist && !f.steps.some((s) => s.trim())) out.push('chores.errors.checklist')
  out.push(...scheduleProblems(f.schedule))
  out.push(...assignmentProblems(f.assignment))
  return out
}
