import type { components } from '@/api/schema'

export type StreakMilestone = components['schemas']['StreakMilestone']

export type HouseholdFieldErrors = Partial<
  Record<
    | 'name'
    | 'timezone'
    | 'dayEnd'
    | 'weekStart'
    | 'siblingVisibility'
    | 'mediaRetentionDays'
    | 'autoApproveHours'
    | 'offlineGraceMinutes'
    | 'defaultChoreMinutes'
    | 'streakMilestones',
    true
  >
>

const FIELD =
  /^body\.(name|timezone|dayEnd|weekStart|siblingVisibility|mediaRetentionDays|autoApproveHours|offlineGraceMinutes|defaultChoreMinutes|streakMilestones)(?:\[\d+\](?:\.\w+)?)?$/

/** Which household controls the server's 422 field errors point at (the view shows its own text). */
export function householdFieldErrors(
  errors: { location?: string; message?: string }[] | null | undefined,
): HouseholdFieldErrors {
  const out: HouseholdFieldErrors = {}
  for (const e of errors ?? []) {
    const key = FIELD.exec(e.location ?? '')?.[1] as keyof HouseholdFieldErrors | undefined
    if (key) out[key] = true
  }
  return out
}

/** Sorted by days, as the server stores them. Returns a new list. */
export function sortMilestones(list: StreakMilestone[]): StreakMilestone[] {
  return [...list].sort((a, b) => a.days - b.days)
}

/** i18n key for the first problem in a milestone list, or undefined when it is valid. */
export function milestoneProblem(list: StreakMilestone[]): string | undefined {
  const days = new Set<number>()
  for (const m of list) {
    if (!Number.isInteger(m.days) || m.days < 1 || m.days > 3650) return 'settings.milestoneDays'
    if (!Number.isInteger(m.points) || m.points < 1 || m.points > 100000)
      return 'settings.milestonePoints'
    if (days.has(m.days)) return 'settings.milestoneDuplicate'
    days.add(m.days)
  }
  if (list.length > 20) return 'settings.milestoneMax'
  return undefined
}
