import { describe, expect, it } from 'vitest'
import {
  buildRequest,
  householdProblems,
  kidColors,
  kidProblems,
  newForm,
  newKid,
  ownerProblems,
  timeZones,
  todayISO,
} from '@/setup/wizard'

describe('setup wizard', () => {
  it('starts with the browser timezone, midnight day end and Monday weeks', () => {
    const f = newForm()
    expect(f.household.timezone).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone)
    expect(f.household).toMatchObject({ dayEnd: '00:00', weekStart: 'monday' })
    expect(timeZones()).toContain(f.household.timezone)
    expect(timeZones()).toContain('UTC')
  })

  it('checks the household step', () => {
    const h = { name: ' ', timezone: '', dayEnd: '24:00', weekStart: 'monday' as const }
    expect(householdProblems(h)).toEqual([
      'setup.household.nameRequired',
      'setup.household.timezoneRequired',
      'setup.household.dayEndInvalid',
    ])
    expect(householdProblems({ ...h, name: 'Home', timezone: 'UTC', dayEnd: '21:30' })).toEqual([])
  })

  it('checks the owner step', () => {
    const o = {
      displayName: 'Mom',
      username: 'mom',
      password: 'long enough',
      confirm: 'long enough',
    }
    expect(ownerProblems(o)).toEqual([])
    expect(ownerProblems({ ...o, username: 'Mo' })).toEqual(['setup.owner.usernameInvalid'])
    expect(ownerProblems({ ...o, password: 'short', confirm: 'short' })).toEqual([
      'setup.owner.passwordTooShort',
    ])
    expect(ownerProblems({ ...o, confirm: 'different' })).toEqual(['setup.owner.passwordMismatch'])
  })

  it('checks kids and gives each new kid an unused color', () => {
    const first = newKid([])
    const second = newKid([first])
    expect(first.color).toBe(kidColors[0])
    expect(second.color).toBe(kidColors[1])
    expect(
      kidProblems(
        [
          { ...first, displayName: '' },
          { ...second, displayName: 'Sam', birthDate: '2099-01-01' },
        ],
        '2026-10-31',
      ),
    ).toEqual(['setup.kids.nameRequired', 'setup.kids.birthDateFuture'])
  })

  it('builds the request, trimming names and omitting empty optional fields', () => {
    const f = newForm()
    f.household.name = ' The Bradys '
    f.owner = { displayName: ' Mom ', username: 'mom', password: 'secret123', confirm: 'secret123' }
    f.kids = [
      { displayName: ' Sam ', birthDate: '', color: '#3b82f6', pinRequired: false },
      { displayName: 'Alex', birthDate: '2019-05-01', color: '#22c55e', pinRequired: true },
    ]
    const req = buildRequest('tok', f)
    expect(req.token).toBe('tok')
    expect(req.household.name).toBe('The Bradys')
    expect(req.owner).toEqual({ displayName: 'Mom', username: 'mom', password: 'secret123' })
    expect(req.kids).toEqual([
      { displayName: 'Sam', color: '#3b82f6' },
      { displayName: 'Alex', color: '#22c55e', birthDate: '2019-05-01', pinRequired: true },
    ])
  })

  it('formats today as a civil date', () => {
    expect(todayISO(new Date(2026, 0, 5))).toBe('2026-01-05')
  })
})
