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
  return out
}
