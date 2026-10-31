import type { components } from '@/api/schema'

export type SetupRequest = components['schemas']['SetupRequest']
export type WeekStart = SetupRequest['household']['weekStart']

/** Kid accent colors offered in the wizard (Tailwind 500 shades: distinct, readable on white). */
export const kidColors = [
  '#3b82f6',
  '#22c55e',
  '#f97316',
  '#a855f7',
  '#ec4899',
  '#14b8a6',
  '#eab308',
  '#ef4444',
] as const

export const weekStarts: NonNullable<WeekStart>[] = [
  'monday',
  'tuesday',
  'wednesday',
  'thursday',
  'friday',
  'saturday',
  'sunday',
]

/** Same rule as the server: 3-32 lowercase letters, digits, dot, underscore or hyphen. */
export const usernamePattern = /^[a-z0-9][a-z0-9._-]{2,31}$/
export const minPasswordLength = 8

export interface KidForm {
  displayName: string
  birthDate: string // YYYY-MM-DD or ''
  color: string
  pinRequired: boolean
}

export interface SetupForm {
  household: { name: string; timezone: string; dayEnd: string; weekStart: NonNullable<WeekStart> }
  owner: { displayName: string; username: string; password: string; confirm: string }
  kids: KidForm[]
}

export function browserTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
}

/** IANA zones the browser knows, always including UTC and the browser's own zone. */
export function timeZones(): string[] {
  const zones = new Set(Intl.supportedValuesOf?.('timeZone') ?? [])
  zones.add('UTC')
  zones.add(browserTimeZone())
  return [...zones].sort()
}

export function newForm(): SetupForm {
  return {
    household: { name: '', timezone: browserTimeZone(), dayEnd: '00:00', weekStart: 'monday' },
    owner: { displayName: '', username: '', password: '', confirm: '' },
    kids: [],
  }
}

/** A new kid row, with the first color not already used. */
export function newKid(existing: KidForm[]): KidForm {
  const used = new Set(existing.map((k) => k.color))
  const color = kidColors.find((c) => !used.has(c)) ?? kidColors[existing.length % kidColors.length]
  return { displayName: '', birthDate: '', color, pinRequired: false }
}

/** i18n keys of problems that block leaving a step; empty when it may continue. */
export function householdProblems(f: SetupForm['household']): string[] {
  const out: string[] = []
  if (!f.name.trim()) out.push('setup.household.nameRequired')
  if (!f.timezone) out.push('setup.household.timezoneRequired')
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(f.dayEnd)) out.push('setup.household.dayEndInvalid')
  return out
}

export function ownerProblems(f: SetupForm['owner']): string[] {
  const out: string[] = []
  if (!f.displayName.trim()) out.push('setup.owner.displayNameRequired')
  if (!usernamePattern.test(f.username)) out.push('setup.owner.usernameInvalid')
  if (f.password.length < minPasswordLength) out.push('setup.owner.passwordTooShort')
  else if (f.password !== f.confirm) out.push('setup.owner.passwordMismatch')
  return out
}

export function kidProblems(kids: KidForm[], today: string): string[] {
  const out = new Set<string>()
  for (const k of kids) {
    if (!k.displayName.trim()) out.add('setup.kids.nameRequired')
    if (k.birthDate && k.birthDate > today) out.add('setup.kids.birthDateFuture')
  }
  return [...out]
}

/** The request body for POST /setup. */
export function buildRequest(token: string, f: SetupForm): SetupRequest {
  return {
    token,
    household: { ...f.household, name: f.household.name.trim() },
    owner: {
      displayName: f.owner.displayName.trim(),
      username: f.owner.username,
      password: f.owner.password,
    },
    kids: f.kids.map((k) => ({
      displayName: k.displayName.trim(),
      color: k.color,
      ...(k.birthDate ? { birthDate: k.birthDate } : {}),
      ...(k.pinRequired ? { pinRequired: true } : {}),
    })),
  }
}

/** Today's civil date in the browser's zone (YYYY-MM-DD), for birth date checks. */
export function todayISO(now = new Date()): string {
  const y = now.getFullYear()
  const m = String(now.getMonth() + 1).padStart(2, '0')
  const d = String(now.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}
