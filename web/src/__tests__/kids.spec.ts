import { describe, expect, it } from 'vitest'
import { ageOn, emptyForm, formFromKid, initials, inputFromForm, type Kid } from '@/kids/form'

const kid: Kid = {
  id: 3,
  slug: 'sam',
  displayName: 'Sam',
  color: '#3b82f6',
  avatar: 'pixel-02',
  birthDate: '2018-04-02',
  pinRequired: true,
  pinSet: false,
  dayEndOverride: '19:30',
  quietHours: { start: '20:00', end: '07:00' },
  schoolHours: { days: ['monday', 'friday'], start: '08:00', end: '15:00' },
}

describe('kid form', () => {
  it('round-trips a kid', () => {
    expect(inputFromForm(formFromKid(kid))).toEqual({
      displayName: 'Sam',
      slug: 'sam',
      color: '#3b82f6',
      avatar: 'pixel-02',
      birthDate: '2018-04-02',
      pinRequired: true,
      dayEndOverride: '19:30',
      quietHours: { start: '20:00', end: '07:00' },
      schoolHours: { days: ['monday', 'friday'], start: '08:00', end: '15:00' },
    })
  })

  it('leaves out sections that are off, so the server clears them', () => {
    const f = formFromKid(kid)
    f.dayEndOn = f.quietOn = f.schoolOn = false
    f.avatar = ''
    f.birthDate = ''
    f.pinRequired = false
    expect(inputFromForm(f)).toEqual({ displayName: 'Sam', slug: 'sam', color: '#3b82f6' })
  })

  it('omits school hours with no days', () => {
    const f = { ...emptyForm(), displayName: ' Alex ', schoolOn: true, schoolDays: [] }
    expect(inputFromForm(f)).toEqual({ displayName: 'Alex', color: '#3b82f6' })
  })

  it('computes ages and initials', () => {
    expect(ageOn('2018-04-02', '2026-04-01')).toBe(7)
    expect(ageOn('2018-04-02', '2026-04-02')).toBe(8)
    expect(ageOn('', '2026-04-02')).toBeUndefined()
    expect(initials(' mary  jane watson ')).toBe('MJ')
  })
})
