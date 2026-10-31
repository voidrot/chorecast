import type { components } from '@/api/schema'
import { kidColors } from '@/setup/wizard'

export type Kid = components['schemas']['Kid']
export type KidInput = components['schemas']['KidInput']

export const weekdayNames = [
  'monday',
  'tuesday',
  'wednesday',
  'thursday',
  'friday',
  'saturday',
  'sunday',
] as const

/** The edit form's state: every optional section has an on/off switch. */
export interface KidForm {
  displayName: string
  slug: string
  color: string
  avatar: string
  birthDate: string
  pinRequired: boolean
  dayEndOn: boolean
  dayEnd: string
  quietOn: boolean
  quietStart: string
  quietEnd: string
  schoolOn: boolean
  schoolDays: string[]
  schoolStart: string
  schoolEnd: string
}

export function emptyForm(avatar = ''): KidForm {
  return {
    displayName: '',
    slug: '',
    color: kidColors[0],
    avatar,
    birthDate: '',
    pinRequired: false,
    dayEndOn: false,
    dayEnd: '19:30',
    quietOn: false,
    quietStart: '20:00',
    quietEnd: '07:00',
    schoolOn: false,
    schoolDays: ['monday', 'tuesday', 'wednesday', 'thursday', 'friday'],
    schoolStart: '08:00',
    schoolEnd: '15:00',
  }
}

export function formFromKid(k: Kid): KidForm {
  const f = emptyForm()
  return {
    ...f,
    displayName: k.displayName,
    slug: k.slug,
    color: k.color,
    avatar: k.avatar ?? '',
    birthDate: k.birthDate ?? '',
    pinRequired: k.pinRequired,
    dayEndOn: !!k.dayEndOverride,
    dayEnd: k.dayEndOverride || f.dayEnd,
    quietOn: !!k.quietHours,
    quietStart: k.quietHours?.start ?? f.quietStart,
    quietEnd: k.quietHours?.end ?? f.quietEnd,
    schoolOn: !!k.schoolHours,
    schoolDays: k.schoolHours?.days ?? f.schoolDays,
    schoolStart: k.schoolHours?.start ?? f.schoolStart,
    schoolEnd: k.schoolHours?.end ?? f.schoolEnd,
  }
}

/** The request body: sections that are off are left out, which clears them on the server. */
export function inputFromForm(f: KidForm): KidInput {
  return {
    displayName: f.displayName.trim(),
    color: f.color,
    ...(f.slug.trim() ? { slug: f.slug.trim() } : {}),
    ...(f.avatar ? { avatar: f.avatar } : {}),
    ...(f.birthDate ? { birthDate: f.birthDate } : {}),
    ...(f.pinRequired ? { pinRequired: true } : {}),
    ...(f.dayEndOn ? { dayEndOverride: f.dayEnd } : {}),
    ...(f.quietOn ? { quietHours: { start: f.quietStart, end: f.quietEnd } } : {}),
    ...(f.schoolOn && f.schoolDays.length
      ? { schoolHours: { days: [...f.schoolDays], start: f.schoolStart, end: f.schoolEnd } }
      : {}),
  }
}

/** Whole years between a YYYY-MM-DD birth date and today (YYYY-MM-DD). */
export function ageOn(birthDate: string, today: string): number | undefined {
  if (!birthDate) return undefined
  const [by, bm, bd] = birthDate.split('-').map(Number)
  const [ty, tm, td] = today.split('-').map(Number)
  let age = ty - by
  if (tm < bm || (tm === bm && td < bd)) age--
  return age >= 0 ? age : undefined
}

/** Initials for kids without an avatar. */
export function initials(name: string): string {
  return name
    .trim()
    .split(/\s+/)
    .slice(0, 2)
    .map((w) => w[0]?.toUpperCase() ?? '')
    .join('')
}
